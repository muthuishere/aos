package groups

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// coverStyleFlags are the look flags shared by `draw cover` and `cover update`.
var coverStyleFlags = []string{
	"color", "text", "text-color", "font", "font-scale", "size", "weight", "align", "position",
	"margin", "border", "border-color", "radius", "preset",
}

var coverValueFlags = append([]string{
	"rect", "app", "title", "monitor", "find", "source", "role", "nth", "pad", "id", "duration", "preview",
}, coverStyleFlags...)

var coverBoolFlags = []string{"block-input", "follow", "no-follow", "detach", "json", "no-config", "exact"}

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "draw", Name: "cover",
				Summary: "Black out part of the screen: a rect, a window, part of a window, or text found in it; follows its window",
				Args: "(--rect=<x,y,w,h> | --monitor=<n> | <app> | --app=<name> | --title=<s> [--rect=<x,y,w,h|N%>] [--find=<text> [--role=<r>] [--nth=<n>] [--pad=<px>]]) " +
					"[--color=<name|#hex|#rrggbbaa>] [--text=<s>] [--text-color=<c>] [--font=<name>] [--font-scale=<k>] [--size=S|M|L|XL] [--weight=regular|bold] " +
					"[--align=left|center|right] [--position=center|top-left|...] [--margin=<px|N%>] [--border=<px>] [--border-color=<c>] [--radius=<px>] [--preset=<name>] " +
					"[--block-input] [--no-follow] [--id=<name>] [--duration=<seconds>] [--detach] [--preview=<out.png>] [--json]",
				Examples: []string{
					`aos draw cover --app=Preview --detach --id=scan`,
					`aos draw cover --app=Mail --rect=0,0,30%,100% --preset=private --detach`,
					`aos draw cover --app=Terminal --find=ghp_ --pad=6 --color=#1e40af --text="Hidden by request" --detach`,
					`aos draw cover --rect=100,100,400,200 --block-input --detach --id=form`,
					`aos draw cover update --id=scan --preset=brb --text="Back in 5"`,
					"aos draw cover list",
					"aos draw cover stop --id=scan",
					"# window covers follow the window: move, resize, other monitor, hidden while minimised",
					"# --detach keeps the cover until `draw cover stop` / `draw stop`; without it the cover shows for --duration (8s)",
					"# presets: private, redacted, brb, plus \"presets\" in `aos draw config`",
				},
				Run: runDrawCover,
			},
			&cli.Command{
				Group: "draw", Name: "cover list",
				Summary:  "List the detached covers, grouped by window",
				Args:     "[--json]",
				Examples: []string{"aos draw cover list"},
				Run:      runDrawCoverList,
			},
			&cli.Command{
				Group: "draw", Name: "cover stop",
				Summary:  "Remove detached covers (all, or one --id)",
				Args:     "[--id=<name>] [--json]",
				Examples: []string{"aos draw cover stop --id=scan", "aos draw cover stop"},
				Run:      runDrawCoverStop,
			},
			&cli.Command{
				Group: "draw", Name: "cover update",
				Summary: "Change a live cover's colour or text in place; the area underneath never shows",
				Args: "--id=<name> [--color=<c>] [--text=<s>] [--text-color=<c>] [--size=S|M|L|XL] [--font=<name>] [--weight=<w>] " +
					"[--align=<a>] [--position=<p>] [--margin=<m>] [--border=<px>] [--border-color=<c>] [--radius=<px>] [--preset=<name>] [--json]",
				Examples: []string{`aos draw cover update --id=scan --text="Private" --color=#000`},
				Run:      runDrawCoverUpdate,
			},
		)
	})
}

// coverStyleFrom reads the look flags into a style (a preset is resolved
// by the engine; for `update` it is resolved here).
func coverStyleFrom(set *argSet) (engine.CoverStyle, error) {
	s := engine.CoverStyle{
		Color: set.String("color", ""), Text: set.String("text", ""), TextColor: set.String("text-color", ""),
		Font: set.String("font", ""), Size: set.String("size", ""), Weight: set.String("weight", ""),
		Align: set.String("align", ""), Position: set.String("position", ""), Margin: set.String("margin", ""),
		BorderColor: set.String("border-color", ""),
	}
	for _, f := range []struct {
		name string
		dst  *float64
	}{{"font-scale", &s.FontScale}, {"border", &s.Border}, {"radius", &s.Radius}} {
		raw := strings.TrimSuffix(set.String(f.name, ""), "px")
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 {
			return s, fmt.Errorf("--%s must be a number ≥ 0, got %q", f.name, raw)
		}
		*f.dst = v
	}
	return s, nil
}

func coverOptionsFrom(args []string) (engine.CoverOptions, *argSet, error) {
	var o engine.CoverOptions
	set, err := parseArgs(args, coverValueFlags...)
	if err != nil {
		return o, nil, err
	}
	if err := set.Reject(append(append([]string{}, coverValueFlags...), coverBoolFlags...)...); err != nil {
		return o, nil, err
	}
	if o.Style, err = coverStyleFrom(set); err != nil {
		return o, nil, err
	}
	o.Preset = set.String("preset", "")
	o.NoConfig = set.Has("no-config")
	o.BlockInput = set.Has("block-input")
	o.NoFollow = set.Has("no-follow")
	if set.Has("follow") && o.NoFollow {
		return o, nil, fmt.Errorf("--follow and --no-follow contradict each other")
	}
	o.ID = set.String("id", "")
	o.Find = set.String("find", "")
	o.FindSource = engine.FindSource(set.String("source", ""))
	o.FindRole = set.String("role", "")
	o.FindExact = set.Has("exact")
	if o.FindNth, err = set.Int("nth", 0); err != nil {
		return o, nil, err
	}
	if o.Pad, err = set.Int("pad", 4); err != nil {
		return o, nil, err
	}
	if o.Monitor, err = set.IntPtr("monitor"); err != nil {
		return o, nil, err
	}
	if raw := set.String("rect", ""); raw != "" {
		r, err := engine.ParseCoverRect(raw)
		if err != nil {
			return o, nil, err
		}
		o.Rect = &r
	}
	match := engine.Match{App: set.String("app", ""), Title: set.String("title", "")}
	if match.App == "" && match.Title == "" && len(set.Rest) > 0 {
		match.App = strings.Join(set.Rest, " ")
	}
	if match.App != "" || match.Title != "" {
		o.Match = &match
	}
	secs := 8.0
	if set.Has("detach") {
		secs = 0 // until `cover stop`: a cover must not lapse on its own
	}
	if raw := set.String("duration", ""); raw != "" {
		if secs, err = strconv.ParseFloat(raw, 64); err != nil || secs < 0 {
			return o, nil, fmt.Errorf("--duration must be seconds (0 = until `draw cover stop`), got %q", raw)
		}
	}
	o.Duration = time.Duration(secs * float64(time.Second))
	return o, set, nil
}

func runDrawCover(c *cli.Ctx, args []string) error {
	o, set, err := coverOptionsFrom(args)
	if err != nil {
		return err
	}
	if o.Match != nil {
		if resolved, err := resolveMatch(*o.Match); err == nil {
			o.Match = &resolved
		}
	}
	if out := set.String("preview", ""); out != "" {
		res, err := engine.RenderCover(o, expandHome(out))
		if err != nil {
			return notFoundExit(err)
		}
		return emitCover(c, set, res, out)
	}

	if set.Has("detach") {
		child := append([]string{"draw", "cover"}, withoutDetach(args)...)
		if o.ID == "" {
			o.ID = "cover-" + strconv.FormatInt(time.Now().UnixMilli()%1_000_000, 36)
			child = append(child, "--id", o.ID)
		}
		if !set.Has("duration") {
			child = append(child, "--duration", "0")
		}
		res, pid, err := engine.SpawnDetachedCover(child)
		if err != nil {
			return &cli.ExitError{Code: 1, Message: err.Error()}
		}
		// Same id: the new cover is up, so the old one can go with no gap.
		engine.StopCovers(o.ID, pid)
		if set.Has("json") {
			return json.NewEncoder(c.Stdout).Encode(res)
		}
		c.Printf("cover %s up at %s until `aos draw cover stop --id=%s`\n", res.ID, fmtRect(res.Rect), res.ID)
		return nil
	}

	if c.Source == "mcp" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		return runChild(c, self, append([]string{"draw", "cover"}, args...))
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() { <-sig; close(stop) }()
	o.Stop = stop

	if engine.IsDrawChild() {
		o.Moved = engine.RegisterCover
		o.Updates = engine.TakeCoverUpdate
		o.UpdateShown = engine.AckCoverUpdate
		defer engine.UnregisterDrawing()
	} else if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		first := true
		o.Moved = func(r engine.CoverResult) {
			if first {
				first = false
				_ = enc.Encode(r)
			}
		}
	}
	if _, err := engine.Cover(o); err != nil {
		return notFoundExit(err)
	}
	return nil
}

func emitCover(c *cli.Ctx, set *argSet, res engine.CoverResult, preview string) error {
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(res)
	}
	c.Println(preview)
	return nil
}

func fmtRect(r engine.Rect) string { return fmt.Sprintf("%d,%d %dx%d", r.X, r.Y, r.W, r.H) }

func runDrawCoverList(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	covers := engine.LiveCovers()
	sort.Slice(covers, func(i, j int) bool { return coverGroup(covers[i]) < coverGroup(covers[j]) })
	if set.Has("json") {
		if covers == nil {
			covers = []engine.LiveCover{}
		}
		return json.NewEncoder(c.Stdout).Encode(covers)
	}
	if len(covers) == 0 {
		c.Println("no covers")
		return nil
	}
	last := "\x00"
	for _, lc := range covers {
		if g := coverGroup(lc); g != last {
			c.Println(g)
			last = g
		}
		r := lc.Cover
		var notes []string
		if r.Hidden {
			notes = append(notes, "hidden (window off screen)")
		}
		if r.Follow {
			notes = append(notes, "follows")
		}
		if r.BlockInput {
			notes = append(notes, "blocks input")
		}
		if r.Tint {
			notes = append(notes, "tint, not redaction")
		}
		look := r.Style.Color
		if look == "" {
			look = "black"
		}
		if r.Style.Text != "" {
			look += fmt.Sprintf(" %q", r.Style.Text)
		}
		c.Printf("  %-16s %-22s %s  %s\n", r.ID, fmtRect(r.Rect), look, strings.Join(notes, ", "))
	}
	return nil
}

func coverGroup(lc engine.LiveCover) string {
	if w := lc.Cover.Window; w != nil {
		return fmt.Sprintf("%s — %s (window %s)", w.App, w.Title, w.ID)
	}
	return "screen"
}

func runDrawCoverStop(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "json"); err != nil {
		return err
	}
	n := engine.StopCovers(set.String("id", ""), 0)
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(map[string]int{"Stopped": n})
	}
	c.Println(fmt.Sprintf("stopped %d cover(s)", n))
	return nil
}

func runDrawCoverUpdate(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, append([]string{"id"}, coverStyleFlags...)...)
	if err != nil {
		return err
	}
	if err := set.Reject(append([]string{"id", "json"}, coverStyleFlags...)...); err != nil {
		return err
	}
	id := set.String("id", "")
	if id == "" {
		return fmt.Errorf("`draw cover update` needs --id=<name> (see `aos draw cover list`)")
	}
	patch, err := coverStyleFrom(set)
	if err != nil {
		return err
	}
	if name := set.String("preset", ""); name != "" {
		p, err := engine.CoverPreset(name)
		if err != nil {
			return err
		}
		patch = p.Merge(patch)
	}
	if patch == (engine.CoverStyle{}) {
		return fmt.Errorf("`draw cover update` needs something to change: --color, --text, --preset, ...")
	}
	n, err := engine.SendCoverUpdate(id, patch)
	if err != nil {
		return notFoundExit(err)
	}
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(map[string]int{"Updated": n})
	}
	c.Println(fmt.Sprintf("updated %d cover(s)", n))
	return nil
}
