package groups

import (
	"encoding/json"
	"errors"
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
				Args:    "[<x> <y>] [--button=left|right|middle] [--double] [--monitor=<n>] | --app=<name>|--title=<s> --find=<text> [--role=<r>] [--focus] [--json]",
				Examples: []string{
					"aos mouse click",
					`aos mouse click --app=Calculator --find=7 --role=button   # AXPress / UIA Invoke: no pointer, no focus`,
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
				Summary: "Type text; with --app/--title it goes to that window without needing focus",
				Args:    "<text...> [--app=<name>|--title=<substring> [--verify] [--focus] [--json]]",
				Examples: []string{
					`aos key type "hello world"`,
					`aos key type "hello" --app=TextEdit --verify   # posted to the app, read back from its text field`,
					`aos key type "hello" --app=Chrome --focus      # real focus + verify, focus handed back after`,
					"# exit 1 if --verify cannot see the text arrive; exit 3 if no window matches",
				},
				Run: runKeyType,
			},
			&cli.Command{
				Group: "key", Name: "press",
				Summary: "Press a key combination",
				Args:    "<combo> [--app=<name>|--title=<substring> [--focus] [--json]]",
				Examples: []string{
					"aos key press cmd+shift+s",
					"aos key press enter --app=TextEdit   # posted to the app, no focus change",
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
	if err := engine.MouseMove(monitor, x, y); err != nil {
		return err
	}
	if gx, gy, err := engine.CursorPosition(); err == nil {
		noteVideoPoint(c, gx, gy)
	}
	return nil
}

// clickRoute is the mouse click decision, pure for tests: "element" when a
// window and --find are given (accessibility press, no pointer), "pointer"
// otherwise (the unchanged coordinate click).
func clickRoute(set *argSet) (string, error) {
	named := namesWindow(set)
	find := set.String("find", "")
	switch {
	case find != "" && !named:
		return "", fmt.Errorf("--find needs the window to search: --app=<name> or --title=<s>")
	case find != "" && len(set.Rest) > 0:
		return "", fmt.Errorf("give either x y or --find, not both")
	case find != "":
		return "element", nil
	case named || set.Has("focus"):
		return "", fmt.Errorf("--app/--title/--focus on `mouse click` go with --find=<text>")
	}
	return "pointer", nil
}

func runMouseClick(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "monitor", "button", "app", "title", "find", "role")
	if err != nil {
		return err
	}
	if err := set.Reject("monitor", "button", "double", "app", "title", "find", "role", "focus", "json"); err != nil {
		return err
	}
	route, err := clickRoute(set)
	if err != nil {
		return err
	}
	if route == "element" {
		match, _ := optionalMatch(set)
		res, err := engine.ClickFound(engine.FindOptions{Match: match, Text: set.String("find", ""), Role: set.String("role", "")}, set.Has("focus"))
		if err != nil {
			return notFoundExit(err)
		}
		r := res.Found.Bounds
		noteVideoTarget(c, "mouse click", res.Window)
		noteVideo(c, "click", "mouse click", &r, res.Found.Label)
		if set.Has("json") {
			return json.NewEncoder(c.Stdout).Encode(res)
		}
		c.Printf("%s  %q at %d,%d\n", res.Route, res.Found.Label, res.Found.Center.X, res.Found.Center.Y)
		return nil
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

// inputOptionsFrom reads the shared focus-free input flags.
func inputOptionsFrom(set *argSet) engine.InputOptions {
	return engine.InputOptions{Focus: set.Has("focus"), Verify: set.Has("verify")}
}

// keyRoute decides which path a key command takes. Pure, for tests:
// "target" means engine.TypeTo / PressTo (focus-free unless --focus),
// "frontmost" means plain keystrokes to whatever has focus. --focus and
// --verify only make sense with a named window.
func keyRoute(set *argSet) (string, error) {
	if !namesWindow(set) {
		if set.Has("focus") || set.Has("verify") {
			return "", fmt.Errorf("--focus and --verify need a target window: --app=<name> or --title=<s>")
		}
		return "frontmost", nil
	}
	return "target", nil
}

func emitInput(c *cli.Ctx, set *argSet, res engine.InputResult) error {
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(res)
	}
	line := res.Route
	if res.Reason != "" {
		line += " (" + res.Reason + ")"
	}
	if res.Verified {
		line += "  verified"
	}
	c.Println(line)
	return nil
}

func inputExit(err error) error {
	if errors.Is(err, engine.ErrNoMatch) {
		return &cli.ExitError{Code: exitNotFound, Message: err.Error()}
	}
	return err
}

func runKeyType(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title", "focus", "verify", "json"); err != nil {
		return err
	}
	text := strings.Join(set.Rest, " ")
	if text == "" {
		return fmt.Errorf("`key type` needs some text")
	}
	route, err := keyRoute(set)
	if err != nil {
		return err
	}
	if route == "frontmost" {
		return engine.TypeText(text)
	}
	// With a window named, input goes to that window's process and never to
	// whatever happens to be frontmost (the person may be typing elsewhere).
	match, _ := optionalMatch(set)
	res, err := engine.TypeTo(match, text, inputOptionsFrom(set))
	if err != nil {
		return inputExit(err)
	}
	noteVideoTarget(c, "key type", res.Window)
	return emitInput(c, set, res)
}

func runKeyPress(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title", "focus", "json"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`key press` takes one combo, such as cmd+shift+s")
	}
	combo := set.Rest[0]
	route, err := keyRoute(set)
	if err != nil {
		return err
	}
	if route == "frontmost" {
		return engine.PressKey(combo)
	}
	match, _ := optionalMatch(set)
	res, err := engine.PressTo(match, combo, inputOptionsFrom(set))
	if err != nil {
		return inputExit(err)
	}
	noteVideoTarget(c, "key press", res.Window)
	return emitInput(c, set, res)
}

// namesWindow reports whether --app or --title was given (no lookup).
func namesWindow(set *argSet) bool {
	return set.String("app", "") != "" || set.String("title", "") != ""
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
