package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("permission", "OS permissions the window and input commands need")
		r.Add(
			&cli.Command{
				Group: "permission", Name: "check",
				Summary:  "Report whether accessibility and screen capture are granted",
				Examples: []string{"aos permission check"},
				Run:      runPermissionCheck,
			},
			&cli.Command{
				Group: "permission", Name: "request",
				Summary: "Ask the OS for the permissions that are missing",
				Args:    "[accessibility|screen]",
				Examples: []string{
					"aos permission request",
					"aos permission request screen",
				},
				Run: runPermissionRequest,
			},
		)
	})
}

func runPermissionCheck(c *cli.Ctx, _ []string) error {
	accessibility := engine.CheckAccessibility()
	screen := engine.CheckScreenCapture()

	c.Printf("accessibility   %s\n", granted(accessibility))
	c.Printf("screen capture  %s\n", granted(screen))
	if !accessibility || !screen {
		return &cli.ExitError{Code: 1}
	}
	return nil
}

func runPermissionRequest(c *cli.Ctx, args []string) error {
	which := "all"
	if len(args) == 1 {
		which = args[0]
	} else if len(args) > 1 {
		return &cli.ExitError{Code: 1, Message: "`permission request` takes at most one of: accessibility, screen"}
	}

	switch which {
	case "all", "accessibility":
		if err := engine.RequestAccessibility(); err != nil && which == "accessibility" {
			return err
		}
		if which == "accessibility" {
			return nil
		}
		fallthrough
	case "screen":
		return engine.RequestScreenCapture()
	}
	return &cli.ExitError{Code: 1, Message: "expected accessibility or screen, got " + which}
}

func granted(ok bool) string {
	if ok {
		return "granted"
	}
	return "denied"
}
