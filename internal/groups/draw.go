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
	"github.com/muthuishere/windowctl"
)

// drawValueFlags are the `draw` flags that take a value.
var drawValueFlags = []string{
	"at", "rect", "app", "title", "monitor", "find", "source", "role", "nth", "text",
	"style", "shape", "size", "color", "text-color", "border", "corners", "from", "font",
	"duration", "preview",
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
					"[--duration=<seconds>] [--detach] [--preview=<out.png>] [--json]",
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

// drawOptionsFrom turns `draw` flags into windowctl's options. Every rule about
// targets, enums and defaults stays in windowctl; this only translates.
func drawOptionsFrom(args []string) (windowctl.DrawOptions, *argSet, error) {
	var opts windowctl.DrawOptions
	set, err := parseArgs(args, drawValueFlags...)
	if err != nil {
		return opts, nil, err
	}
	if err := set.Reject(append(append([]string{}, drawValueFlags...), drawBoolFlags...)...); err != nil {
		return opts, nil, err
	}

	opts.Text = set.String("text", "")
	opts.Mouse = set.Has("mouse")
	opts.Style = windowctl.DrawStyle(set.String("style", ""))
	opts.Shape = windowctl.DrawShape(set.String("shape", ""))
	opts.Size = windowctl.DrawSize(strings.ToUpper(set.String("size", "")))
	opts.Border = windowctl.DrawBorder(set.String("border", ""))
	opts.Corners = windowctl.DrawCorners(set.String("corners", ""))
	opts.From = windowctl.DrawFrom(set.String("from", ""))
	opts.Font = set.String("font", "")
	opts.NoConfig = set.Has("no-config")
	opts.Find = set.String("find", "")
	opts.FindSource = windowctl.FindSource(set.String("source", ""))
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
		opts.Point = &windowctl.Point{X: n[0], Y: n[1]}
	}
	if raw := set.String("rect", ""); raw != "" {
		n, err := drawInts(raw, 4, "--rect=x,y,w,h")
		if err != nil {
			return opts, nil, err
		}
		opts.Region = &windowctl.Rect{X: n[0], Y: n[1], W: n[2], H: n[3]}
	}
	match := windowctl.Match{App: set.String("app", ""), Title: set.String("title", "")}
	if match.App == "" && match.Title == "" && len(set.Rest) > 0 {
		match.App = strings.Join(set.Rest, " ")
	}
	if match.App != "" || match.Title != "" {
		opts.Match = &match
	}

	for _, c := range []struct {
		flag string
		dst  **windowctl.Color
	}{{"color", &opts.Color}, {"text-color", &opts.TextColor}} {
		raw := set.String(c.flag, "")
		if raw == "" {
			continue
		}
		col, err := windowctl.ParseColor(raw)
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
	if opts.Match != nil {
		if resolved, err := resolveMatch(*opts.Match); err == nil {
			opts.Match = &resolved
		}
	}

	if out := set.String("preview", ""); out != "" {
		res, err := windowctl.RenderDraw(opts, expandHome(out))
		if err != nil {
			return notFoundExit(err)
		}
		return emitDraw(c, set, res, out)
	}

	// --detach re-runs this command as a detached child that registers in
	// windowctl's shared drawing registry, so `aos draw stop` (and
	// windowctl's) can end it and new drawings keep clear of its sign.
	if set.Has("detach") {
		child := append([]string{"draw"}, withoutDetach(args)...)
		if !set.Has("duration") {
			child = append(child, "--duration", "300")
		}
		res, err := windowctl.SpawnDetachedDraw(child)
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
		opts.Avoid = windowctl.LiveDrawingSigns()
	}

	// AppKit only draws from the main goroutine. A terminal invocation is on
	// it; an MCP tool call is not, so re-run this same command as a child
	// process, whose main goroutine is free.
	if c.Source == "mcp" {
		self, err := os.Executable()
		if err != nil {
			return err
		}
		return runChild(c, self, append([]string{"draw"}, args...))
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	go func() { <-sig; close(stop) }()
	opts.Stop = stop

	if set.Has("json") || windowctl.IsDrawChild() {
		// Place first, so the caller learns where it lands before it shows.
		res, err := windowctl.RenderDraw(opts, os.DevNull)
		if err != nil {
			return notFoundExit(err)
		}
		if windowctl.IsDrawChild() {
			windowctl.RegisterDrawing(res)
			defer windowctl.UnregisterDrawing()
		} else if err := emitDraw(c, set, res, ""); err != nil {
			return err
		}
	}
	if _, err := windowctl.Draw(opts); err != nil {
		return notFoundExit(err)
	}
	return nil
}

func emitDraw(c *cli.Ctx, set *argSet, res windowctl.DrawResult, preview string) error {
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
	n := windowctl.StopDrawings()
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
		path, err := windowctl.InitDrawConfig(set.Has("force"))
		if err != nil {
			return err
		}
		c.Println(path)
		return nil
	}
	cfg, err := windowctl.LoadDrawConfig()
	if err != nil {
		return err
	}
	c.Printf("# %s\n", windowctl.DrawConfigPath())
	enc := json.NewEncoder(c.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func runDrawFonts(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`draw fonts` takes no arguments")
	}
	for _, f := range windowctl.ListFonts() {
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
