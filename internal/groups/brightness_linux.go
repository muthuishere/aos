package groups

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/sys"
)

func backlightDir() string {
	dirs, _ := filepath.Glob("/sys/class/backlight/*")
	if len(dirs) == 0 {
		return ""
	}
	return dirs[0]
}

func readSysInt(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

func getBacklight() (int, error) {
	if sys.Has("brightnessctl") {
		if out, err := sys.Output("brightnessctl", "-m", "info"); err == nil {
			return parseBrightnessctl(out)
		}
	}
	dir := backlightDir()
	cur, err := readSysInt(filepath.Join(dir, "brightness"))
	if err != nil {
		return 0, err
	}
	max, err := readSysInt(filepath.Join(dir, "max_brightness"))
	if err != nil {
		return 0, err
	}
	return toPercent(cur, max), nil
}

// setBacklight prefers brightnessctl, which goes through logind and needs no
// root; writing sysfs directly works only as root or with a udev rule.
func setBacklight(pct int) error {
	if sys.Has("brightnessctl") {
		_, err := sys.Output("brightnessctl", "-q", "set", strconv.Itoa(pct)+"%")
		return err
	}
	dir := backlightDir()
	max, err := readSysInt(filepath.Join(dir, "max_brightness"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "brightness"), []byte(strconv.Itoa(fromPercent(pct, max))), 0o644)
}

// monitorName is the xrandr output name (eDP-1, HDMI-1), the only name X11
// gives without reading EDID; ddcutil's model name replaces it when known.
func monitorName(m engine.Monitor) string {
	if o, err := xrandrFor(m); err == nil {
		return o.Name
	}
	return fmt.Sprintf("Display %d", m.ID)
}

func isPanelOutput(name string) bool {
	for _, p := range []string{"eDP", "LVDS", "DSI"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// brightnessScreens drives a laptop panel through the backlight and, when
// ddcutil is installed, external monitors over DDC/CI.
func brightnessScreens(ms []engine.Monitor) ([]screen, error) {
	outs, _ := xrandrOutputs()
	var ddc []ddcutilDisplay
	if sys.Has("ddcutil") {
		if out, err := sys.Output("ddcutil", "detect", "--terse"); err == nil {
			ddc = parseDdcutilDetect(out)
		}
	}
	hasBacklight := backlightDir() != "" || sys.Has("brightnessctl")
	res := make([]screen, 0, len(ms))
	for _, m := range ms {
		s := screen{ID: m.ID, Name: fmt.Sprintf("Display %d", m.ID)}
		output := ""
		for _, o := range outs {
			if o.X == m.X && o.Y == m.Y {
				output = o.Name
			}
		}
		if output != "" {
			s.Name = output
		}
		switch {
		case isPanelOutput(output) || (output == "" && len(ms) == 1 && hasBacklight):
			s.Method = "builtin"
			if !hasBacklight {
				s.Reason = "no backlight found (needs brightnessctl or /sys/class/backlight)"
				break
			}
			s.get, s.set = getBacklight, setBacklight
		default:
			s.Method = "ddc"
			d, ok := matchDdcutil(ddc, output)
			if !ok {
				s.Reason = "no DDC/CI route (install ddcutil and load i2c-dev)"
				if output == "" {
					s.Reason = "cannot match this monitor to an output (needs xrandr)"
				}
				break
			}
			if d.Model != "" {
				s.Name = d.Model
			}
			num := strconv.Itoa(d.Number)
			s.get = func() (int, error) {
				out, err := sys.Output("ddcutil", "--display", num, "getvcp", "10", "--brief")
				if err != nil {
					return 0, err
				}
				return parseDdcutilVCP(out)
			}
			s.set = func(pct int) error {
				out, err := sys.Output("ddcutil", "--display", num, "getvcp", "10", "--brief")
				if err != nil {
					return err
				}
				max, err := ddcutilMax(out)
				if err != nil {
					return err
				}
				_, err = sys.Output("ddcutil", "--display", num, "setvcp", "10", strconv.Itoa(fromPercent(pct, max)))
				return err
			}
		}
		res = append(res, s)
	}
	return res, nil
}
