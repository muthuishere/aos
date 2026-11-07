package groups

import (
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// awakeCommand holds a systemd inhibitor lock for as long as `sleep` runs.
func awakeCommand(d time.Duration, display bool) (string, []string, error) {
	if !sys.Has("systemd-inhibit") {
		return "", nil, &cli.ExitError{Code: 2, Message: "`power awake` needs systemd-inhibit (systemd), which this machine does not have"}
	}
	return "systemd-inhibit", inhibitArgs(d, display), nil
}

func runPowerAwakeHold(_ *cli.Ctx, _ []string) error { return errUnsupported }
