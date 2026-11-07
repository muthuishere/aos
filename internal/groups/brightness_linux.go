package groups

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

var errNoBacklight = &cli.ExitError{Code: 2, Message: "no backlight found (needs brightnessctl or /sys/class/backlight)"}

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

func getBrightness() (int, error) {
	if sys.Has("brightnessctl") {
		if out, err := sys.Output("brightnessctl", "-m", "info"); err == nil {
			return parseBrightnessctl(out)
		}
	}
	dir := backlightDir()
	if dir == "" {
		return 0, errNoBacklight
	}
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

// setBrightness prefers brightnessctl, which goes through logind and needs no
// root; writing sysfs directly works only as root or with a udev rule.
func setBrightness(pct int) error {
	if sys.Has("brightnessctl") {
		_, err := sys.Output("brightnessctl", "-q", "set", strconv.Itoa(pct)+"%")
		return err
	}
	dir := backlightDir()
	if dir == "" {
		return errNoBacklight
	}
	max, err := readSysInt(filepath.Join(dir, "max_brightness"))
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "brightness"), []byte(strconv.Itoa(fromPercent(pct, max))), 0o644)
}
