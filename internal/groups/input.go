package groups

import (
	"fmt"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("mouse", "Pointer position and clicks")
		r.Add(
			&cli.Command{
				Group: "mouse", Name: "move",
				Summary: "Move the pointer",
				Args:    "<x> <y> [--monitor=<n>]",
				Examples: []string{
					"aos mouse move 400 300",
					"aos mouse move 400 300 --monitor=2",
				},
				Run: runMouseMove,
			},
			&cli.Command{
				Group: "mouse", Name: "click",
				Summary: "Click, optionally moving to a point first",
				Args:    "[<x> <y>] [--button=left|right|middle] [--double] [--monitor=<n>]",
				Examples: []string{
					"aos mouse click",
					"aos mouse click 400 300 --double",
					"aos mouse click --button=right",
				},
				Run: runMouseClick,
			},
			&cli.Command{
				Group: "mouse", Name: "position",
				Summary:  "Print the pointer position as `x y`",
				Examples: []string{"aos mouse position     # 784 539"},
				Run: func(c *cli.Ctx, _ []string) error {
					x, y, err := engine.CursorPosition()
					if err != nil {
						return err
					}
					c.Printf("%d %d\n", x, y)
					return nil
				},
			},
		)

		r.Describe("key", "Keystrokes and typed text")
		r.Add(
			&cli.Command{
				Group: "key", Name: "type",
				Summary: "Type text, optionally into a named window",
				Args:    "<text...> [--app=<name>|--title=<substring>]",
				Examples: []string{
					`aos key type "hello world"`,
					`aos key type "hello" --app=Chrome`,
				},
				Run: runKeyType,
			},
			&cli.Command{
				Group: "key", Name: "press",
				Summary: "Press a key combination",
				Args:    "<combo> [--app=<name>|--title=<substring>]",
				Examples: []string{
					"aos key press cmd+shift+s",
					"aos key press enter --app=Chrome",
				},
				Run: runKeyPress,
			},
		)
	})
}

func runMouseMove(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "monitor")
	if err != nil {
		return err
	}
	if err := set.Reject("monitor"); err != nil {
		return err
	}
	if len(set.Rest) != 2 {
		return fmt.Errorf("`mouse move` takes an x and a y")
	}
	x, err := parseInt(set.Rest[0])
	if err != nil {
		return fmt.Errorf("bad x %q", set.Rest[0])
	}
	y, err := parseInt(set.Rest[1])
	if err != nil {
		return fmt.Errorf("bad y %q", set.Rest[1])
	}
	monitor, err := set.IntPtr("monitor")
	if err != nil {
		return err
	}
	return engine.MouseMove(monitor, x, y)
}

func runMouseClick(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "monitor", "button")
	if err != nil {
		return err
	}
	if err := set.Reject("monitor", "button", "double"); err != nil {
		return err
	}

	opts := engine.ClickOptions{Double: set.Has("double")}
	switch button := set.String("button", "left"); button {
	case "left":
		opts.Button = engine.MouseLeft
	case "right":
		opts.Button = engine.MouseRight
	case "middle":
		opts.Button = engine.MouseMiddle
	default:
		return fmt.Errorf("--button must be left, right, or middle, got %q", button)
	}

	switch len(set.Rest) {
	case 0:
		// Click wherever the pointer already is.
	case 2:
		x, err := parseInt(set.Rest[0])
		if err != nil {
			return fmt.Errorf("bad x %q", set.Rest[0])
		}
		y, err := parseInt(set.Rest[1])
		if err != nil {
			return fmt.Errorf("bad y %q", set.Rest[1])
		}
		opts.X, opts.Y = &x, &y
	default:
		return fmt.Errorf("`mouse click` takes either no coordinates or both x and y")
	}

	monitor, err := set.IntPtr("monitor")
	if err != nil {
		return err
	}
	opts.Monitor = monitor
	if err := engine.MouseClick(opts); err != nil {
		return err
	}
	noteVideoClick(c)
	return nil
}

func runKeyType(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title"); err != nil {
		return err
	}
	text := strings.Join(set.Rest, " ")
	if text == "" {
		return fmt.Errorf("`key type` needs some text")
	}
	// With a window named, the engine focuses and verifies before typing, so
	// keystrokes cannot land in whatever happened to be frontmost.
	if match, ok := optionalMatch(set); ok {
		if err := engine.TypeInto(match, text); err != nil {
			return err
		}
		noteVideoWindow(c, "key type", match)
		return nil
	}
	return engine.TypeText(text)
}

func runKeyPress(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`key press` takes one combo, such as cmd+shift+s")
	}
	combo := set.Rest[0]
	if match, ok := optionalMatch(set); ok {
		if err := engine.PressKeyInto(match, combo); err != nil {
			return err
		}
		noteVideoWindow(c, "key press", match)
		return nil
	}
	return engine.PressKey(combo)
}

// optionalMatch reads --app / --title when the command's positionals are its
// payload rather than a window name.
//
// It resolves a short app name the same way the window commands do. This is the
// shared path for `capture screenshot --app=`, `key type --app=` and
// `key press --app=`, all of which are documented with `--app=Chrome` — which
// matched nothing on macOS, where the app is named "Google Chrome", until the
// resolution moved in here.
func optionalMatch(set *argSet) (engine.Match, bool) {
	match := engine.Match{App: set.String("app", ""), Title: set.String("title", "")}
	if match.App != "" && match.Title == "" {
		if app, ok := resolveAppName(match.App); ok {
			match.App = app
		}
	}
	return match, match.App != "" || match.Title != ""
}
