package groups

import (
	"os/exec"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/sys"
)

// GNOME's Do Not Disturb is the inverse of notifications.show-banners.
const gnomeNotifications = "org.gnome.desktop.notifications"

func needGnomeDND() error {
	if !sys.Has("gsettings") {
		return focusUnsupported("Do Not Disturb is supported on GNOME (gsettings), which this desktop does not have")
	}
	return nil
}

func readFocus() (focusState, error) {
	if err := needGnomeDND(); err != nil {
		return focusState{}, err
	}
	out, err := sys.Output("gsettings", "get", gnomeNotifications, "show-banners")
	if err != nil {
		return focusState{}, err
	}
	return focusState{On: strings.TrimSpace(out) == "false"}, nil
}

// setFocus flips show-banners. With a duration, a detached shell turns
// banners back on when it ends, surviving the CLI.
func setFocus(on bool, d time.Duration) error {
	if err := needGnomeDND(); err != nil {
		return err
	}
	if _, err := sys.Output("gsettings", "set", gnomeNotifications, "show-banners", gnomeBanners(on)); err != nil {
		return err
	}
	if on && d > 0 {
		cmd := exec.Command("sh", "-c", focusRestoreScript(d))
		detach(cmd)
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
	}
	return nil
}
