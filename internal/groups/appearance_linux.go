package groups

import (
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// Linux appearance is GNOME's (and GNOME-compatible desktops') gsettings
// keys; other desktops have no common switch, so they exit 2.

var errNoGsettings = &cli.ExitError{Code: 2, Message: "appearance needs gsettings (GNOME or a compatible desktop)"}

func gsettings(args ...string) (string, error) {
	if !sys.Has("gsettings") {
		return "", errNoGsettings
	}
	return sys.Output("gsettings", args...)
}

func getDarkMode() (bool, error) {
	out, err := gsettings("get", "org.gnome.desktop.interface", "color-scheme")
	if err != nil {
		return false, err
	}
	return gnomeColorScheme(out), nil
}

func setDarkMode(on bool) error {
	scheme := "default"
	if on {
		scheme = "prefer-dark"
	}
	_, err := gsettings("set", "org.gnome.desktop.interface", "color-scheme", scheme)
	return err
}

const nightLightSchema = "org.gnome.settings-daemon.plugins.color"

func getNightLight() (bool, error) {
	out, err := gsettings("get", nightLightSchema, "night-light-enabled")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "true", nil
}

func setNightLight(on bool) error {
	v := "false"
	if on {
		v = "true"
	}
	_, err := gsettings("set", nightLightSchema, "night-light-enabled", v)
	return err
}
