package groups

import (
	"os"
	"strconv"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows"
)

const (
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
	esContinuous      = 0x80000000
)

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// awakeCommand re-runs aos as the holder: the execution-state assertion
// belongs to a thread, so something has to stay alive to keep it.
func awakeCommand(d time.Duration, display bool) (string, []string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	args := []string{"power", "awake-hold"}
	if d > 0 {
		args = append(args, "--seconds="+strconv.Itoa(int(d.Seconds())))
	}
	if display {
		args = append(args, "--display")
	}
	return exe, args, nil
}

func runPowerAwakeHold(_ *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "seconds")
	if err != nil {
		return err
	}
	secs, err := set.Int("seconds", 0)
	if err != nil {
		return err
	}
	flags := uintptr(esContinuous | esSystemRequired)
	if set.Has("display") {
		flags |= esDisplayRequired
	}
	if r, _, err := procSetThreadExecutionState.Call(flags); r == 0 {
		return err
	}
	if secs > 0 {
		time.Sleep(time.Duration(secs) * time.Second)
		return nil
	}
	select {} // until killed by `power awake off`
}
