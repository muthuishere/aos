package groups

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/sys"
)

func isLinux() bool { return true }

// windowManagers are the lightweight WMs worth starting on a virtual display,
// most-preferred first. Without one, X11 apps open unmanaged and unmovable, so
// `window move` has nothing to talk to.
var windowManagers = []string{"openbox", "i3", "fluxbox", "mutter", "xfwm4"}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 tests for existence without disturbing the process.
	return process.Signal(syscall.Signal(0)) == nil
}

func runHeadlessStart(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "display", "size")
	if err != nil {
		return err
	}
	if err := set.Reject("display", "size", "wm"); err != nil {
		return err
	}
	if !sys.Has("Xvfb") {
		return &cli.ExitError{Code: 1, Message: "Xvfb is not installed; `aos pkg install xvfb`"}
	}
	if state, running := readState(); running {
		return &cli.ExitError{Code: 1,
			Message: fmt.Sprintf("a display is already running on %s (pid %d); stop it first", state.Display, state.PID)}
	}

	display := set.String("display", ":99")
	size := set.String("size", "1920x1080")

	// Xvfb wants WxHxDEPTH; 24-bit is what every screenshot path expects.
	xvfb := exec.Command("Xvfb", display, "-screen", "0", size+"x24", "-nolisten", "tcp")
	xvfb.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := xvfb.Start(); err != nil {
		return err
	}

	state := headlessState{Display: display, Size: size, PID: xvfb.Process.Pid}

	// Give the server a moment to accept connections before anything asks it
	// for a window list.
	time.Sleep(500 * time.Millisecond)
	if !processAlive(state.PID) {
		return &cli.ExitError{Code: 1, Message: "Xvfb exited immediately; is " + display + " already in use?"}
	}

	if set.Has("wm") {
		if wm := sys.FirstAvailable(windowManagers...); wm != "" {
			manager := exec.Command(wm)
			manager.Env = append(os.Environ(), "DISPLAY="+display)
			manager.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := manager.Start(); err == nil {
				state.WM, state.WMPID = wm, manager.Process.Pid
			}
		}
	}

	if state.WM != "" {
		// The window backend asks the WM for the client list, and a WM that has
		// not finished claiming the screen answers with an error. Waiting here
		// means the very next command works, instead of failing once on a race.
		os.Setenv("DISPLAY", display)
		if !waitForWindowManager(3 * time.Second) {
			c.Warnf("%s did not become ready in time; window commands may need a retry\n", state.WM)
		}
	}

	if err := writeState(state); err != nil {
		return err
	}
	c.Printf("display  %s  %s  pid %d\n", state.Display, state.Size, state.PID)
	if state.WM != "" {
		c.Printf("wm       %s  pid %d\n", state.WM, state.WMPID)
	} else if set.Has("wm") {
		c.Warnf("no window manager found; install one of: %v\n", windowManagers)
	}
	c.Printf("use      export DISPLAY=%s   (or `aos headless run <cmd>`)\n", state.Display)
	return nil
}

// waitForWindowManager polls until the window backend can talk to the WM.
func waitForWindowManager(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := engine.ListWindows(engine.Filter{}); err == nil {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func runHeadlessStop(c *cli.Ctx, _ []string) error {
	state, running := readState()
	if state.Display == "" {
		return &cli.ExitError{Code: 1, Message: "no managed display recorded"}
	}
	if running {
		for _, pid := range []int{state.WMPID, state.PID} {
			if pid > 0 {
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Signal(syscall.SIGTERM)
				}
			}
		}
	}
	if err := clearState(); err != nil {
		return err
	}
	c.Printf("stopped %s\n", state.Display)
	return nil
}
