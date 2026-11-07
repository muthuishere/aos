package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/sys"
)

// showDesktop toggles the EWMH "showing desktop" state through wmctrl, and
// falls back to the Super+D shortcut most desktops bind.
func showDesktop() error {
	if sys.Has("wmctrl") {
		state, _ := sys.Output("wmctrl", "-m")
		return sysRun("wmctrl", "-k", desktopToggle(state))
	}
	return engine.PressKey("super+d")
}

func sysRun(name string, args ...string) error {
	_, err := sys.Output(name, args...)
	return err
}

func minimizeWindow(id string) error {
	if !sys.Has("xdotool") {
		return &cli.ExitError{Code: 2, Message: "`app hide-others` needs xdotool on Linux; install it"}
	}
	return sysRun("xdotool", "windowminimize", id)
}

func closeWindow(id string) error {
	if !sys.Has("wmctrl") {
		return &cli.ExitError{Code: 2, Message: "`app quit-all` needs wmctrl on Linux; install it"}
	}
	return sysRun("wmctrl", "-ic", id)
}
