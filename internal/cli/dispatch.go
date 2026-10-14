package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/muthuishere/aos/internal/obs"
)

// Run dispatches argv and returns a process exit code, recording one span for
// the invocation. Every path through the CLI and every MCP tool call arrives
// here, so this is the single place work has to be accounted for.
func Run(c *Ctx, args []string) int {
	started := time.Now()
	exit := dispatch(c, args)
	c.record(args, exit, started)
	return exit
}

// record turns a finished invocation into a span. The command's own arguments
// are counted, not copied: they routinely carry file paths, message bodies, and
// anything else a person typed, and telemetry is not the place for it.
func (c *Ctx) record(args []string, exit int, started time.Time) {
	if c.Recorder == nil {
		return
	}
	route, extra := describeInvocation(c.Registry, args)
	status := obs.Status{Code: obs.StatusOK}
	if exit != 0 {
		status = obs.Status{Code: obs.StatusError, Message: fmt.Sprintf("exit status %d", exit)}
	}
	source := c.Source
	if source == "" {
		source = "cli"
	}
	ended := time.Now()

	c.Recorder.Record(obs.Span{
		Name:       route,
		Kind:       "SPAN_KIND_SERVER",
		StartNanos: started.UnixNano(),
		EndNanos:   ended.UnixNano(),
		Status:     status,
		Attributes: []obs.Attribute{
			obs.StringAttr("agentic_os.route", route),
			obs.StringAttr("agentic_os.source", source),
			obs.IntAttr("agentic_os.arg_count", int64(extra)),
			obs.IntAttr("agentic_os.exit_code", int64(exit)),
			obs.IntAttr("agentic_os.duration_ms", ended.Sub(started).Milliseconds()),
			obs.BoolAttr("agentic_os.ok", exit == 0),
		},
	})
}

// describeInvocation resolves what was actually run, so spans are grouped by
// route rather than by the exact words typed.
func describeInvocation(r *Registry, args []string) (string, int) {
	if len(args) == 0 {
		return "help", 0
	}
	if cmd, rest, err := r.Resolve(args); err == nil && cmd != nil {
		return cmd.Route(), len(rest)
	}
	// An unresolved command is still worth recording; the first word is enough
	// to spot a typo an agent keeps making.
	return args[0], len(args) - 1
}

func dispatch(c *Ctx, args []string) int {
	args, hostFlag, err := stripHostFlag(args)
	if err != nil {
		c.Warnf("aos: %v\n", err)
		return 2
	}
	if len(args) == 0 || isHelpFlag(args[0]) {
		PrintRootHelp(c.Stdout, c.Registry)
		return 0
	}

	switch args[0] {
	case "version", "--version", "-v":
		c.Println(c.Version)
		return 0
	case "commands":
		if err := runCommands(c, args[1:]); err != nil {
			c.Warnf("aos: %v\n", err)
			return 1
		}
		return 0
	}

	cmd, rest, err := c.Registry.Resolve(args)
	if err != nil {
		var groupHelp *GroupHelpError
		if errors.As(err, &groupHelp) {
			PrintGroupHelp(c.Stdout, groupHelp.Group, c.GOOS, HasDisplay(c.Env, c.GOOS))
			return 0
		}
		c.Warnf("aos: %v\n", err)
		return 1
	}

	// `aos <group> --help` when the group also has a default command.
	if len(rest) == 1 && isHelpFlag(rest[0]) {
		if cmd.Name == "" {
			if g := c.Registry.Group(cmd.Group); g != nil && len(visibleCommands(g)) > 1 {
				PrintGroupHelp(c.Stdout, g, c.GOOS, HasDisplay(c.Env, c.GOOS))
				return 0
			}
		}
		PrintCommandHelp(c.Stdout, cmd, c.GOOS, HasDisplay(c.Env, c.GOOS))
		return 0
	}

	// Screen commands run on the host, which may be another machine. This
	// comes before the platform and display checks: those are the host's.
	if cmd.FollowsHost && !c.Local {
		host, err := c.ResolveHost(hostFlag)
		if err != nil {
			c.Warnf("aos: %q runs on the machine with the screen: %v\n", cmd.Route(), err)
			return 2
		}
		if host.Kind != HostLocal {
			return c.runOnHost(host, cmd, args[:len(args)-len(rest)], rest)
		}
	}

	if !cmd.Supports(c.GOOS) {
		c.Warnf("aos: %v\n", &ErrUnsupported{Route: cmd.Route(), GOOS: c.GOOS, On: cmd.Platforms})
		return 2
	}

	if cmd.NeedsDisplay && !HasDisplay(c.Env, c.GOOS) {
		c.Warnf("aos: %q needs a display, and this machine has none\n", cmd.Route())
		c.Warnf("Start one with `aos headless start`, or check `aos headless status`.\n")
		return 2
	}

	if cmd.Run == nil && cmd.Binary != "" {
		return runExternal(c, cmd, rest)
	}
	if cmd.Run == nil {
		c.Warnf("aos: %q is registered but not implemented yet\n", cmd.Route())
		return 3
	}

	if err := cmd.Run(c, rest); err != nil {
		var exit *ExitError
		if errors.As(err, &exit) {
			if exit.Message != "" {
				c.Warnf("aos: %s\n", exit.Message)
			}
			return exit.Code
		}
		c.Warnf("aos: %v\n", err)
		return 1
	}
	return 0
}

// ExitError lets a Runner choose the process exit code.
type ExitError struct {
	Code    int
	Message string
}

func (e *ExitError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Message
}

func isHelpFlag(s string) bool {
	return s == "--help" || s == "-h" || s == "help"
}
