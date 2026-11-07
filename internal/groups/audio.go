package groups

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("audio", "Volume, mute, and output/input devices")
		r.Add(
			&cli.Command{
				Group: "audio", Name: "volume",
				Summary: "Print the output volume, or set it",
				Args:    "[<0-100>|+<n>|-<n>]",
				Examples: []string{
					"aos audio volume",
					"aos audio volume 40",
					"aos audio volume +5",
				},
				Run: runAudioVolume,
			},
			&cli.Command{
				Group: "audio", Name: "mute",
				Summary:  "Print or change the output mute state",
				Args:     "[on|off|toggle]",
				Examples: []string{"aos audio mute toggle"},
				Run:      runAudioMute,
			},
		)
	})
}

func runAudioVolume(c *cli.Ctx, args []string) error {
	if len(args) == 0 {
		level, err := getVolume()
		if err != nil {
			return err
		}
		c.Printf("%d\n", level)
		return nil
	}

	arg := args[0]
	target, err := strconv.Atoi(strings.TrimPrefix(arg, "+"))
	if err != nil {
		return fmt.Errorf("volume must be a number, got %q", arg)
	}
	// A leading + or - is relative; a bare number is absolute.
	if strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-") {
		current, err := getVolume()
		if err != nil {
			return err
		}
		target += current
	}
	target = clamp(target, 0, 100)
	if err := setVolume(target); err != nil {
		return err
	}
	c.Printf("%d\n", target)
	return nil
}

func runAudioMute(c *cli.Ctx, args []string) error {
	if len(args) == 0 {
		muted, err := getMute()
		if err != nil {
			return err
		}
		c.Println(onOff(muted))
		return nil
	}

	var want bool
	switch args[0] {
	case "on", "true", "yes":
		want = true
	case "off", "false", "no":
		want = false
	case "toggle":
		muted, err := getMute()
		if err != nil {
			return err
		}
		want = !muted
	default:
		return fmt.Errorf("expected on, off, or toggle, got %q", args[0])
	}
	if err := setMute(want); err != nil {
		return err
	}
	c.Println(onOff(want))
	return nil
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
