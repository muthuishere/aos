package groups

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "display", Name: "brightness",
			Summary: "Print or set screen brightness 0-100 (the built-in panel, else the main display)",
			Args:    "[<0-100>|+<n>|-<n>]",
			Examples: []string{
				"aos display brightness       # 70",
				"aos display brightness 50",
				"aos display brightness -10",
			},
			Run: runDisplayBrightness,
		})
	})
}

func runDisplayBrightness(c *cli.Ctx, args []string) error {
	if len(args) > 1 {
		return fmt.Errorf("`display brightness` takes at most one level")
	}
	current, err := getBrightness()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		c.Printf("%d\n", current)
		return nil
	}
	target, err := brightnessTarget(args[0], current)
	if err != nil {
		return err
	}
	if err := setBrightness(target); err != nil {
		return err
	}
	c.Printf("%d\n", target)
	return nil
}

// brightnessTarget resolves "50", "+10" or "-10" against the current level,
// clamped to 0-100: the same grammar as `audio volume`.
func brightnessTarget(arg string, current int) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "+"))
	if err != nil {
		return 0, fmt.Errorf("brightness must be a number, got %q", arg)
	}
	if strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-") {
		n += current
	}
	return clamp(n, 0, 100), nil
}

// toPercent and fromPercent convert between a device's raw scale and 0-100.
func toPercent(raw, max int) int {
	if max <= 0 {
		return 0
	}
	return int(math.Round(float64(raw) * 100 / float64(max)))
}

func fromPercent(pct, max int) int {
	return int(math.Round(float64(pct) * float64(max) / 100))
}

// parseBrightnessctl reads `brightnessctl -m info`:
// "intel_backlight,backlight,400,42%,960".
func parseBrightnessctl(out string) (int, error) {
	f := strings.Split(strings.TrimSpace(strings.Split(out, "\n")[0]), ",")
	if len(f) < 5 {
		return 0, fmt.Errorf("unexpected brightnessctl output %q", out)
	}
	cur, err1 := strconv.Atoi(f[2])
	max, err2 := strconv.Atoi(f[4])
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("unexpected brightnessctl output %q", out)
	}
	return toPercent(cur, max), nil
}
