package groups

import (
	"fmt"
	"net/url"
	"strings"

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
			Args:    "<app> [args...] [--wait[=<ms>]]",
			Examples: []string{
				`aos launch "Visual Studio Code"`,
				"aos launch Chrome --wait",
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
	rest, timeout, wait, err := takeWaitFlag(args)
	if err != nil {
		return err
	}
	if len(rest) == 0 {
		return fmt.Errorf("`launch` needs an application name")
	}

	app := rest[0]
	if err := launchApp(app, rest[1:]); err != nil {
		return err
	}
	if !wait {
		return nil
	}

	// Launching is asynchronous everywhere, so --wait is what makes `launch`
	// composable: the next command can assume the window is really there.
	window, err := waitForMatch(engine.Match{App: app}, timeout)
	if err != nil {
		return err
	}
	c.Println(describeWindow(window))
	return nil
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
