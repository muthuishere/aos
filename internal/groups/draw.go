package groups

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// drawValueFlags are the `draw` flags that take a value.
var drawValueFlags = []string{
	"at", "rect", "app", "title", "monitor", "find", "source", "role", "nth", "text",
	"style", "shape", "size", "color", "text-color", "border", "corners", "from", "font",
	"duration", "preview", "position", "margin", "offset", "bg", "align", "max-width", "weight",
	"font-scale", "animate",
}

var drawBoolFlags = []string{"mouse", "exact", "detach", "json", "no-config"}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("draw", "Point at something on screen with a big arrow and a sign")
		r.Add(
			&cli.Command{
				Group:   "draw",
				Summary: "Draw a click-through arrow + sign at a point, region, window, text or the mouse",
				Args: "(--at=<x,y> | --rect=<x,y,w,h> | <app> | --app=<name> | --title=<s> | --mouse) [--find=<text> [--role=<r>] [--source=<s>] [--exact] [--nth=<n>]] " +
					"--text=<sign> [--style=arrow|ring|box] [--shape=bend|straight|zigzag|spiral] [--size=S|M|L] [--color=<name|#hex>] [--from=<side>] [--monitor=<n>] " +
					"[--position=<side>] [--duration=<seconds>] [--detach] [--animate=on|off] [--preview=<out.png>] [--json]",
				Examples: []string{
					`aos draw --app=Calculator --find=7 --style=ring --text="press 7"`,
					`aos draw --at=640,400 --text="click here" --duration=5`,
					`aos draw --rect=100,100,400,200 --style=box --text="this area" --preview=/tmp/draw.png`,
					`aos draw --mouse --text="you are here" --detach`,
					"aos draw stop",
					"# exit 3 when --find does not find the window or the text",
				},
				Run: runDraw,
			},
			&cli.Command{
				Group: "draw", Name: "text",
				Summary: "Show a styled text label (no arrow) inside a window, a monitor, a rect, or beside a point",
				Args: "<text...> [--app=<name> | --title=<s> | --monitor=<n> | --rect=<x,y,w,h> | --at=<x,y> | --mouse] " +
					"[--position=bottom-center|top-center|center|left|right|top-left|top-right|bottom-left|bottom-right] [--margin=<px|N%>] [--offset=<dx,dy>] " +
					"[--color=<text colour>] [--bg=auto|none|<colour>] [--font=<name>] [--font-scale=<k>] [--size=S|M|L|XL] [--weight=regular|bold] " +
					"[--align=left|center|right] [--max-width=<px|N%>] [--duration=<seconds>] [--detach] [--preview=<out.png>] [--json]",
				Examples: []string{
					`aos draw text "Step 2: pick a file" --app=Finder --position=top --color=amber`,
					`aos draw text "Done ✓" --monitor=2 --position=center --size=XL --color=green`,
					`aos draw text "Line one\nLine two" --rect=100,100,800,500 --position=bottom-right --bg=none`,
					"# default: bottom-center of the focused monitor (the recording's monitor while `capture video` runs), subtitle safe area (8% bottom, 5% sides)",
				},
				Run: runDrawText,
			},
			&cli.Command{
				Group: "draw", Name: "stop",
				Summary:  "Remove every detached drawing",
				Args:     "[--json]",
				Examples: []string{"aos draw stop"},
				Run:      runDrawStop,
			},
			&cli.Command{
				Group: "draw", Name: "config",
				Summary:  "Show the draw defaults file, or create it with --init",
				Args:     "[--init] [--force]",
				Examples: []string{"aos draw config", "aos draw config --init"},
				Run:      runDrawConfig,
			},
			&cli.Command{
				Group: "draw", Name: "fonts",
				Summary:  "List the font names --font accepts",
				Examples: []string{"aos draw fonts"},
				Run:      runDrawFonts,
			},
		)
	})
}

// drawOptionsFrom turns `draw` flags into the engine's options. Every rule about
// targets, enums and defaults stays in the engine; this only translates.
func drawOptionsFrom(args []string) (engine.DrawOptions, *argSet, error) {
	var opts engine.DrawOptions
	set, err := parseArgs(args, drawValueFlags...)
	if err != nil {
		return opts, nil, err
	}
	if err := set.Reject(append(append([]string{}, drawValueFlags...), drawBoolFlags...)...); err != nil {
		return opts, nil, err
	}

	opts.Text = set.String("text", "")
	opts.Mouse = set.Has("mouse")
	opts.Style = engine.DrawStyle(set.String("style", ""))
	opts.Shape = engine.DrawShape(set.String("shape", ""))
	opts.Size = engine.DrawSize(strings.ToUpper(set.String("size", "")))
	opts.Border = engine.DrawBorder(set.String("border", ""))
	opts.Corners = engine.DrawCorners(set.String("corners", ""))
	opts.From = engine.DrawFrom(set.String("from", ""))
	opts.Position = engine.DrawPosition(set.String("position", ""))
	opts.Margin = set.String("margin", "")
	opts.Background = set.String("bg", "")
	opts.Align = set.String("align", "")
	opts.MaxWidth = set.String("max-width", "")
	opts.Weight = set.String("weight", "")
	opts.Animate = engine.DrawAnimate(set.String("animate", ""))
	if raw := set.String("font-scale", ""); raw != "" {
		if opts.FontScale, err = strconv.ParseFloat(raw, 64); err != nil || opts.FontScale <= 0 {
			return opts, nil, fmt.Errorf("--font-scale must be a positive number, got %q", raw)
		}
	}
	if raw := set.String("offset", ""); raw != "" {
		n, err := drawInts(raw, 2, "--offset=dx,dy")
		if err != nil {
			return opts, nil, err
		}
		opts.Offset = &engine.Point{X: n[0], Y: n[1]}
	}
	opts.Font = set.String("font", "")
	opts.NoConfig = set.Has("no-config")
	opts.Find = set.String("find", "")
	opts.FindSource = engine.FindSource(set.String("source", ""))
	opts.FindRole = set.String("role", "")
	opts.FindExact = set.Has("exact")
	if opts.FindNth, err = set.Int("nth", 0); err != nil {
		return opts, nil, err
	}
	if opts.Monitor, err = set.IntPtr("monitor"); err != nil {
		return opts, nil, err
	}

	if raw := set.String("at", ""); raw != "" {
		n, err := drawInts(raw, 2, "--at=x,y")
		if err != nil {
			return opts, nil, err
		}
		opts.Point = &engine.Point{X: n[0], Y: n[1]}
	}
	if raw := set.String("rect", ""); raw != "" {
		n, err := drawInts(raw, 4, "--rect=x,y,w,h")
		if err != nil {
			return opts, nil, err
		}
		opts.Region = &engine.Rect{X: n[0], Y: n[1], W: n[2], H: n[3]}
	}
	match := engine.Match{App: set.String("app", ""), Title: set.String("title", "")}
	if match.App == "" && match.Title == "" && len(set.Rest) > 0 {
		match.App = strings.Join(set.Rest, " ")
	}
	if match.App != "" || match.Title != "" {
		opts.Match = &match
	}

	for _, c := range []struct {
		flag string
		dst  **engine.Color
	}{{"color", &opts.Color}, {"text-color", &opts.TextColor}} {
		raw := set.String(c.flag, "")
		if raw == "" {
			continue
		}
		col, err := engine.ParseColor(raw)
		if err != nil {
			return opts, nil, fmt.Errorf("--%s: %w", c.flag, err)
		}
		*c.dst = &col
	}

	secs := 8.0
	if set.Has("detach") {
		secs = 300
	}
	if raw := set.String("duration", ""); raw != "" {
		if secs, err = strconv.ParseFloat(raw, 64); err != nil || secs < 0 {
			return opts, nil, fmt.Errorf("--duration must be seconds (0 = until `draw stop`), got %q", raw)
		}
	}
	opts.Duration = time.Duration(secs * float64(time.Second))
	return opts, set, nil
}

func drawInts(raw string, n int, what string) ([]int, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != n {
		return nil, fmt.Errorf("%s needs %d numbers, got %q", what, n, raw)
	}
	out := make([]int, n)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", what, p)
		}
		out[i] = v
	}
	return out, nil
}

func runDraw(c *cli.Ctx, args []string) error {
	opts, set, err := drawOptionsFrom(args)
	if err != nil {
		return err
	}
	return showDraw(c, []string{"draw"}, args, opts, set)
}

// runDrawText is `draw text <text...>`: the positional words are the label
// (not an app name), drawn with the engine's text style.
func runDrawText(c *cli.Ctx, args []string) error {
	opts, set, err := drawOptionsFrom(args)
	if err != nil {
		return err
	}
	if opts.Match != nil && set.String("app", "") == "" && set.String("title", "") == "" {
		opts.Match = nil // the words were the text, not an app
	}
	if opts.Text == "" {
		opts.Text = strings.Join(set.Rest, " ")
	}
	opts.Text = strings.ReplaceAll(opts.Text, `\n`, "\n")
	if strings.TrimSpace(opts.Text) == "" {
		return fmt.Errorf("`draw text` needs the text to show")
	}
	opts.Style = engine.DrawText
	return showDraw(c, []string{"draw", "text"}, args, opts, set)
}

// showDraw previews, detaches or shows a drawing; prefix is the command
// line a detached / MCP child re-runs (e.g. ["draw", "text"]).
func showDraw(c *cli.Ctx, prefix, args []string, opts engine.DrawOptions, set *argSet) error {
	opts = applyRecordingMonitor(c, opts)
	if opts.Match != nil {
		if resolved, err := resolveMatch(*opts.Match); err == nil {
			opts.Match = &resolved
		}
	}

	if out := set.String("preview", ""); out != "" {
		res, err := engine.RenderDraw(opts, expandHome(out))
		if err != nil {
			return notFoundExit(err)
		}
		return emitDraw(c, set, res, out)
	}

	// --detach re-runs this command as a detached child that registers in
	// the engine's shared drawing registry, so `aos draw stop` can end it and
	// new drawings keep clear of its sign.
	if set.Has("detach") {
		child := append(append([]string{}, prefix...), withoutDetach(args)...)
		if !set.Has("duration") {
			child = append(child, "--duration", "300")
		}
		res, err := engine.SpawnDetachedDraw(child)
		if err != nil {
			return &cli.ExitError{Code: 1, Message: err.Error()}
		}
		if set.Has("json") {
			return json.NewEncoder(c.Stdout).Encode(res)
		}
		c.Println("drawing until it times out or `aos draw stop`")
		return nil
	}
	if opts.Avoid == nil {
		opts.Avoid = engine.LiveDrawingSigns()
	}

	// AppKit only draws from the main goroutine. A terminal invocation is on
	// it; an MCP tool call is not, so re-run this same command as a child
	// process, whose main goroutine is free.
	if c.Source == "mcp" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		return runChild(c, self, append(append([]string{}, prefix...), args...))
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() { <-sig; close(stop) }()
	opts.Stop = stop

	placed := false
	if set.Has("json") || engine.IsDrawChild() {
		// Place first, so the caller learns where it lands before it shows.
		res, err := engine.RenderDraw(opts, os.DevNull)
		if err != nil {
			return notFoundExit(err)
		}
		placed = true
		noteVideoMatchTarget(c, "draw", opts.Match)
		noteVideoDraw(c, res, opts.Text)
		if engine.IsDrawChild() {
			engine.RegisterDrawing(res)
			defer engine.UnregisterDrawing()
		} else if err := emitDraw(c, set, res, ""); err != nil {
			return err
		}
	}
	if !placed && activeRecording(c) != nil {
		// Only while recording: place it once more to log where it lands.
		if res, err := engine.RenderDraw(opts, os.DevNull); err == nil {
			noteVideoMatchTarget(c, "draw", opts.Match)
			noteVideoDraw(c, res, opts.Text)
		}
	}
	if _, err := engine.Draw(opts); err != nil {
		return notFoundExit(err)
	}
	return nil
}

func emitDraw(c *cli.Ctx, set *argSet, res engine.DrawResult, preview string) error {
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(res)
	}
	if preview != "" {
		c.Println(preview)
	}
	return nil
}

func runDrawStop(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	n := engine.StopDrawings()
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(map[string]int{"Stopped": n})
	}
	c.Println(fmt.Sprintf("stopped %d drawing(s)", n))
	return nil
}

// runChild runs a command with this invocation's streams and keeps its exit code.
func runChild(c *cli.Ctx, bin string, args []string) error {
	cmd := exec.Command(bin, args...)
	cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
	if err := cmd.Run(); err != nil {
		if code := exitCodeOf(err); code != 0 {
			return &cli.ExitError{Code: code}
		}
		return err
	}
	return nil
}

func runDrawConfig(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("init", "force"); err != nil {
		return err
	}
	if set.Has("init") {
		path, err := engine.InitDrawConfig(set.Has("force"))
		if err != nil {
			return err
		}
		c.Println(path)
		return nil
	}
	cfg, err := engine.LoadDrawConfig()
	if err != nil {
		return err
	}
	c.Printf("# %s\n", engine.DrawConfigPath())
	enc := json.NewEncoder(c.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func runDrawFonts(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`draw fonts` takes no arguments")
	}
	for _, f := range engine.ListFonts() {
		c.Println(f)
	}
	return nil
}

func withoutDetach(args []string) []string {
	var out []string
	for _, a := range args {
		if a == "--detach" || a == "-detach" || strings.HasPrefix(a, "--detach=") || strings.HasPrefix(a, "-detach=") {
			continue
		}
		out = append(out, a)
	}
	return out
}
