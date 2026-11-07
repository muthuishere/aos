package groups

import (
	"fmt"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("appearance", "Dark mode and night light (Night Shift)")
		r.Add(
			&cli.Command{
				Group: "appearance", Name: "dark",
				Summary: "Print or set dark mode: on, off or toggle",
				Args:    "[on|off|toggle]",
				Examples: []string{
					"aos appearance dark          # on | off",
					"aos appearance dark on",
					"aos appearance dark toggle",
				},
				Run: func(c *cli.Ctx, args []string) error {
					return runSwitch(c, "appearance dark", args, getDarkMode, setDarkMode)
				},
			},
			&cli.Command{
				Group: "appearance", Name: "nightlight",
				Summary: "Print or set the warm night colours (Night Shift, Night Light)",
				Args:    "[on|off|toggle]",
				Examples: []string{
					"aos appearance nightlight    # on | off",
					"aos appearance nightlight on",
				},
				Run: func(c *cli.Ctx, args []string) error {
					return runSwitch(c, "appearance nightlight", args, getNightLight, setNightLight)
				},
			},
		)
	})
}

// runSwitch is the shared shape of an on/off setting: bare prints the state,
// on/off/toggle changes it and prints the new state.
func runSwitch(c *cli.Ctx, route string, args []string, get func() (bool, error), set func(bool) error) error {
	if len(args) > 1 {
		return fmt.Errorf("`%s` takes one of on, off, toggle", route)
	}
	current, err := get()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		c.Println(onOff(current))
		return nil
	}
	want, err := switchTarget(args[0], current)
	if err != nil {
		return fmt.Errorf("`%s`: %v", route, err)
	}
	if want != current {
		if err := set(want); err != nil {
			return err
		}
	}
	c.Println(onOff(want))
	return nil
}

// switchTarget resolves on/off/toggle (and true/false, 1/0) against the
// current state.
func switchTarget(arg string, current bool) (bool, error) {
	switch strings.ToLower(arg) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no":
		return false, nil
	case "toggle":
		return !current, nil
	}
	return false, fmt.Errorf("want on, off or toggle, got %q", arg)
}

// gnomeColorScheme maps the GNOME color-scheme setting to dark or not.
// "prefer-dark" is dark; "default" and "prefer-light" are not.
func gnomeColorScheme(value string) bool {
	return strings.Trim(strings.TrimSpace(value), "'") == "prefer-dark"
}
