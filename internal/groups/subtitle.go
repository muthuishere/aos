package groups

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// subtitleRequest is one caption: what to say, for how long, and where on
// screen. It carries no "wait" — blocking is the caller's business, and every
// backend behaves the same way there.
type subtitleRequest struct {
	Text     string
	Seconds  int
	Position string // bottom | top | center
	Size     int    // text height in pixels
}

const (
	subtitleSeconds = 10
	subtitleSize    = 48
)

// subtitleTag marks subtitle drawings in the engine's shared registry, so
// `subtitle stop` and a new caption end only subtitles, never arrows.
const subtitleTag = "subtitle"

var subtitleValueFlags = []string{
	"seconds", "position", "size", "color", "bg", "font", "font-scale", "align", "weight",
	"margin", "max-width", "offset", "app", "title", "monitor", "rect", "at", "preview",
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("subtitle", "Narrate what an agent is doing, on screen, without taking focus")
		r.Add(
			&cli.Command{
				Group: "subtitle", Name: "show",
				Summary: "Show a film-style caption over everything (or over one app's window) for a few seconds",
				Args: "<text...> [--seconds=<n>] [--app=<name> | --title=<s> | --monitor=<n> | --rect=<x,y,w,h>] " +
					"[--position=bottom-center|top-center|center|left|right|top-left|top-right|bottom-left|bottom-right] [--margin=<px|N%>] [--offset=<dx,dy>] " +
					"[--size=S|M|L|XL|<px>] [--color=<text colour>] [--bg=auto|none|<colour>] [--font=<name>] [--weight=regular|bold] [--align=left|center|right] " +
					"[--max-width=<px|N%>] [--detach] [--wait] [--preview=<out.png>] [--json]",
				Examples: []string{
					`aos subtitle show "installing dependencies"`,
					`aos subtitle show "Now I'm opening Safari" --app=Safari --position=bottom`,
					`aos subtitle show "running the migration" --seconds=30 --position=top`,
					`aos subtitle show "step 1 of 3" --seconds=4 --wait`,
					"aos subtitle stop",
					"# white on a translucent dark pill, bottom-center, 8% up; a new caption replaces the last",
				},
				Run: runSubtitleShow,
			},
			&cli.Command{
				Group: "subtitle", Name: "stop",
				Summary:  "Remove the caption on screen",
				Examples: []string{"aos subtitle stop"},
				Run:      runSubtitleStop,
			},
			&cli.Command{
				Group: "subtitle", Name: "test",
				Summary:  "Show a short sample, to check subtitles work on this machine",
				Examples: []string{"aos subtitle test"},
				Run:      runSubtitleTest,
			},
		)
	})
}

// subtitleOptions turns `subtitle show` flags into an engine text drawing.
func subtitleOptions(args []string) (engine.DrawOptions, *argSet, float64, error) {
	var opts engine.DrawOptions
	// --wait / --detach take no value, so they stay out of the value flags:
	// `--wait "text"` must not swallow the caption.
	set, err := parseArgs(args, subtitleValueFlags...)
	if err != nil {
		return opts, nil, 0, err
	}
	if err := set.Reject(append([]string{"wait", "detach", "json", "no-config"}, subtitleValueFlags...)...); err != nil {
		return opts, nil, 0, err
	}
	opts.Text = strings.ReplaceAll(strings.Join(set.Rest, " "), `\n`, "\n")
	opts.Text = strings.ReplaceAll(opts.Text, "\r", "")
	if strings.TrimSpace(opts.Text) == "" {
		return opts, nil, 0, fmt.Errorf("`subtitle show` needs some text to show")
	}
	opts.Style, opts.Subtitle = engine.DrawText, true
	opts.Position = engine.DrawPosition(set.String("position", ""))
	opts.Margin = set.String("margin", "")
	opts.Background = set.String("bg", "")
	opts.Align = set.String("align", "")
	opts.MaxWidth = set.String("max-width", "")
	opts.Weight = set.String("weight", "")
	opts.Font = set.String("font", "")
	opts.NoConfig = set.Has("no-config")
	if raw := set.String("color", ""); raw != "" {
		col, err := engine.ParseColor(raw)
		if err != nil {
			return opts, nil, 0, fmt.Errorf("--color: %w", err)
		}
		opts.Color = &col
	}
	if raw := set.String("font-scale", ""); raw != "" {
		if opts.FontScale, err = strconv.ParseFloat(raw, 64); err != nil || opts.FontScale <= 0 {
			return opts, nil, 0, fmt.Errorf("--font-scale must be a positive number, got %q", raw)
		}
	}
	// --size: S/M/L/XL, or (as before) a text height in pixels.
	if raw := set.String("size", ""); raw != "" {
		if px, err := strconv.Atoi(raw); err == nil {
			if px < 8 || px > 400 {
				return opts, nil, 0, fmt.Errorf("--size must be S, M, L, XL or 8..400 pixels")
			}
			opts.FontSize = float64(px)
		} else {
			opts.Size = engine.DrawSize(strings.ToUpper(raw))
		}
	}
	if opts.Monitor, err = set.IntPtr("monitor"); err != nil {
		return opts, nil, 0, err
	}
	if raw := set.String("rect", ""); raw != "" {
		n, err := drawInts(raw, 4, "--rect=x,y,w,h")
		if err != nil {
			return opts, nil, 0, err
		}
		opts.Region = &engine.Rect{X: n[0], Y: n[1], W: n[2], H: n[3]}
	}
	if raw := set.String("at", ""); raw != "" {
		n, err := drawInts(raw, 2, "--at=x,y")
		if err != nil {
			return opts, nil, 0, err
		}
		opts.Point = &engine.Point{X: n[0], Y: n[1]}
	}
	if raw := set.String("offset", ""); raw != "" {
		n, err := drawInts(raw, 2, "--offset=dx,dy")
		if err != nil {
			return opts, nil, 0, err
		}
		opts.Offset = &engine.Point{X: n[0], Y: n[1]}
	}
	if app, title := set.String("app", ""), set.String("title", ""); app != "" || title != "" {
		m := engine.Match{App: app, Title: title}
		if resolved, err := resolveMatch(m); err == nil {
			m = resolved
		}
		opts.Match = &m
	}
	secs := float64(subtitleSeconds)
	if raw := set.String("seconds", ""); raw != "" {
		if secs, err = strconv.ParseFloat(raw, 64); err != nil || secs < 1 {
			return opts, nil, 0, fmt.Errorf("--seconds must be at least 1")
		}
	}
	opts.Duration = time.Duration(secs * float64(time.Second))
	return opts, set, secs, nil
}

func runSubtitleShow(c *cli.Ctx, args []string) error {
	opts, set, secs, err := subtitleOptions(args)
	if err != nil {
		return err
	}
	opts = applyRecordingMonitor(c, opts)
	if out := set.String("preview", ""); out != "" {
		res, err := engine.RenderDraw(opts, expandHome(out))
		if err != nil {
			return notFoundExit(err)
		}
		return emitDraw(c, set, res, out)
	}
	// The detached child: show it here until it times out or is stopped.
	if engine.IsDrawChild() {
		return showDraw(c, []string{"subtitle", "show"}, args, opts, set)
	}

	// One caption at a time, like film subtitles: the new one replaces the last.
	engine.StopDrawingsTagged(subtitleTag)
	mode := "overlay"
	if runtime.GOOS == "linux" {
		// the engine has no linux overlay yet; keep the original backend there.
		mode, err = showSubtitle(legacySubtitle(opts, secs))
	} else if _, derr := engine.SpawnDetachedDrawTagged(
		append([]string{"subtitle", "show"}, withoutFlag(withoutDetach(args), "wait")...), subtitleTag); derr != nil {
		// Be loud about the downgrade: the original backend may only manage
		// a notification, and the caller must know what is on screen.
		fmt.Fprintf(os.Stderr, "aos subtitle: overlay failed (%v); using the fallback\n", derr)
		mode, err = showSubtitle(legacySubtitle(opts, secs))
	}
	if err != nil {
		return err
	}
	if set.Has("json") {
		// Where it is: the same placement the overlay child computes.
		res, rerr := engine.RenderDraw(opts, os.DevNull)
		if rerr != nil {
			return notFoundExit(rerr)
		}
		if err := json.NewEncoder(c.Stdout).Encode(subtitleJSON{Mode: mode, Seconds: secs, Position: positionName(opts.Position), DrawResult: res}); err != nil {
			return err
		}
	} else {
		c.Printf("%s  %gs  %s\n", mode, secs, positionName(opts.Position))
	}

	// Showing a subtitle returns immediately so it can sit over other work.
	// --wait is for the script that wants the next step to follow this one.
	if set.Has("wait") {
		time.Sleep(opts.Duration)
	}
	return nil
}

// subtitleJSON is `subtitle show --json`: the backend used and where the
// caption sits (Monitor, Sign = the caption's rect, Surface = the monitor).
type subtitleJSON struct {
	Mode     string  `json:"Mode"`
	Seconds  float64 `json:"Seconds"`
	Position string  `json:"Position"`
	engine.DrawResult
}

func positionName(p engine.DrawPosition) string {
	if p == "" {
		return "bottom-center"
	}
	return string(p)
}

// legacySubtitle maps the options onto the original one-line backend.
func legacySubtitle(o engine.DrawOptions, secs float64) subtitleRequest {
	req := subtitleRequest{
		Text:    strings.NewReplacer("\n", " ", "\r", " ").Replace(o.Text),
		Seconds: int(math.Ceil(secs)), Position: "bottom", Size: subtitleSize,
	}
	switch p := string(o.Position); {
	case strings.HasPrefix(p, "top"):
		req.Position = "top"
	case p == "center" || p == "left" || p == "right":
		req.Position = "center"
	}
	switch {
	case o.FontSize > 0:
		req.Size = int(o.FontSize)
	case o.Size == engine.SizeS:
		req.Size = 24
	case o.Size == engine.SizeM:
		req.Size = 34
	case o.Size == engine.SizeXL:
		req.Size = 72
	}
	return req
}

func withoutFlag(args []string, name string) []string {
	var out []string
	for _, a := range args {
		if a == "--"+name || strings.HasPrefix(a, "--"+name+"=") {
			continue
		}
		out = append(out, a)
	}
	return out
}

func runSubtitleStop(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`subtitle stop` takes no arguments")
	}
	c.Println(fmt.Sprintf("stopped %d subtitle(s)", engine.StopDrawingsTagged(subtitleTag)))
	return nil
}

// runSubtitleTest goes through the same parsing as `show`, so what it proves is
// the path a real caption takes rather than a shortcut around it.
func runSubtitleTest(c *cli.Ctx, _ []string) error {
	return runSubtitleShow(c, []string{"aos subtitle works", "--seconds=3"})
}
