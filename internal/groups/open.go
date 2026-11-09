package groups

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("open", "Hand a file, folder, or URL to its default application")
		r.Add(&cli.Command{
			Group:   "open",
			Summary: "Open a file, folder, or URL with the system default handler",
			Args:    "<path-or-url>",
			Examples: []string{
				"aos open https://claude.ai",
				"aos open .",
			},
			Run: runOpen,
		})

		r.Describe("launch", "Start applications")
		r.Add(&cli.Command{
			Group:   "launch",
			Summary: "Launch an application, optionally waiting for its window",
			Args:    "<app> [args...] [--wait[=<ms>]] [--or-focus]",
			Examples: []string{
				`aos launch "Visual Studio Code"`,
				"aos launch Chrome --wait",
				"aos launch Slack --or-focus   # raise Slack's window if it is open, else start it",
			},
			Run: runLaunch,
		})

		r.Describe("webapp", "Open a site as a standalone browser window")
		r.Add(&cli.Command{
			Group: "webapp", Name: "open",
			Summary:  "Open a URL in a chromeless app window",
			Args:     "<url>",
			Examples: []string{"aos webapp open https://app.slack.com"},
			Run:      runWebapp,
		})
	})
}

func runLaunch(c *cli.Ctx, args []string) error {
	// Everything except our own --wait belongs to the application, flags
	// included: `launch Chrome --new-window` must reach Chrome. Parsing the
	// whole line would claim those flags for us and reject them as unknown, so
	// only --wait is lifted out and the rest is passed through untouched.
	rest, orFocus := takeOrFocusFlag(args)
	rest, timeout, wait, err := takeWaitFlag(rest)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("`launch` needs an application name")
	}

	app := rest[0]
	if orFocus {
		// A window of the app is already open: raise it instead of starting
		// a second copy (or a new window) of the app.
		if focused, ok, err := focusOpenApp(app); ok || err != nil {
			if err == nil {
				c.Println(describeWindow(focused))
			}
			return err
		}
	}
	var before map[string]bool
	if wait {
		before = appWindowIDs(app)
	}
	if err := launchApp(app, rest[1:]); err != nil {
		return err
	}
	if !wait {
		return nil
	}

	// Launching is asynchronous everywhere, so --wait is what makes `launch`
	// composable: the next command can assume the window is really there.
	// It waits for a NEW window: with other windows of the app already open
	// (a second Notepad), any match would return before this one exists.
	window, err := waitForNewWindow(app, before, rest[1:], timeout)
	if err != nil {
		return err
	}
	c.Println(describeWindow(window))
	return nil
}

// takeOrFocusFlag removes `--or-focus` from args. Like --wait it is lifted
// out so every other flag still reaches the application.
func takeOrFocusFlag(args []string) (rest []string, orFocus bool) {
	for _, arg := range args {
		if arg == "--or-focus" {
			orFocus = true
			continue
		}
		rest = append(rest, arg)
	}
	return rest, orFocus
}

// focusOpenApp raises the first open window of app. ok is false when the app
// has no window, so the caller launches it instead.
func focusOpenApp(app string) (engine.Window, bool, error) {
	match, err := resolveMatch(engine.Match{App: app})
	if err != nil {
		return engine.Window{}, false, nil
	}
	windows, _ := engine.ListWindows(engine.Filter(match))
	if len(windows) == 0 {
		return engine.Window{}, false, nil
	}
	if err := engine.FocusWindowID(windows[0].ID); err != nil {
		return engine.Window{}, true, err
	}
	return windows[0], true, nil
}

// takeWaitFlag removes `--wait` or `--wait=<ms>` from args, returning what is
// left along with the timeout and whether waiting was asked for.
func takeWaitFlag(args []string) (rest []string, timeout int, wait bool, err error) {
	timeout = 10000
	for _, arg := range args {
		switch {
		case arg == "--wait":
			wait = true
		case strings.HasPrefix(arg, "--wait="):
			value := strings.TrimPrefix(arg, "--wait=")
			parsed, convErr := parseInt(value)
			if convErr != nil || parsed <= 0 {
				return nil, 0, false, fmt.Errorf("--wait wants milliseconds, got %q", value)
			}
			wait, timeout = true, parsed
		default:
			rest = append(rest, arg)
		}
	}
	return rest, timeout, wait, nil
}

func runOpen(c *cli.Ctx, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("`open` needs a path or URL")
	}
	return openTarget(args[0])
}

func runWebapp(c *cli.Ctx, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("`webapp open` needs a URL")
	}
	target := args[0]
	if !strings.Contains(target, "://") {
		target = "https://" + target
	}
	if _, err := url.ParseRequestURI(target); err != nil {
		return fmt.Errorf("not a usable URL: %q", args[0])
	}
	return openWebapp(target)
}

// appWindowIDs are the ids of the app's windows open right now.
func appWindowIDs(app string) map[string]bool {
	ids := map[string]bool{}
	match, err := resolveMatch(engine.Match{App: app})
	if err != nil {
		return ids
	}
	windows, _ := engine.ListWindows(engine.Filter(match))
	for _, w := range windows {
		ids[w.ID] = true
	}
	return ids
}

// waitForNewWindow returns the first window of app that was not open before
// the launch. An app that only brings its existing window forward opens none,
// so after a short grace (or, when files were passed, the full timeout) an
// existing window is accepted, as before.
//
// A file passed to the app also counts by name: Windows 11 Notepad may open it
// as a tab of a window that was already there, which changes that window's
// title but not its id.
func waitForNewWindow(app string, before map[string]bool, args []string, timeoutMS int) (engine.Window, error) {
	withArgs := len(args) > 0
	var names []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			if base := filepath.Base(a); base != "" && base != "." {
				names = append(names, strings.ToLower(base))
			}
		}
	}
	start := time.Now()
	deadline := start.Add(time.Duration(timeoutMS) * time.Millisecond)
	grace := start.Add(1500 * time.Millisecond)
	for {
		if match, err := resolveMatch(engine.Match{App: app}); err == nil {
			windows, _ := engine.ListWindows(engine.Filter(match))
			for _, w := range windows {
				if !before[w.ID] {
					return w, nil
				}
			}
			for _, w := range windows {
				for _, n := range names {
					if strings.Contains(strings.ToLower(w.Title), n) {
						return w, nil
					}
				}
			}
			now := time.Now()
			if len(windows) > 0 && (now.After(deadline) || (!withArgs && now.After(grace))) {
				return windows[0], nil
			}
		}
		if time.Now().After(deadline) {
			return engine.Window{}, fmt.Errorf("no window of %s appeared within %dms", app, timeoutMS)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
