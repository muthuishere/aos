package groups

import (
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// awakeCommand: caffeinate -i blocks idle sleep, -d also keeps the display
// on, -t ends it by itself.
func awakeCommand(d time.Duration, display bool) (string, []string, error) {
	return "caffeinate", caffeinateArgs(d, display), nil
}

func runPowerAwakeHold(_ *cli.Ctx, _ []string) error { return errUnsupported }
