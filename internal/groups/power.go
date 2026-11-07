package groups

import (
	"fmt"

	"github.com/muthuishere/aos/internal/cli"
)

// powerState is the one reading both the `power` and `battery` groups render.
type powerState struct {
	OnAC       bool
	HasBattery bool
	Percent    int    // -1 when unknown
	Status     string // charging, discharging, charged, unknown
	Remaining  string // free-form, "" when the platform does not report it
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("power", "Power source detection and keep-awake")
		r.Add(
			&cli.Command{
				Group: "power", Name: "status",
				Summary:  "Print the current power source (ac or battery)",
				Examples: []string{"aos power status"},
				Run: func(c *cli.Ctx, _ []string) error {
					state, err := readPower()
					if err != nil {
						return err
					}
					if state.OnAC {
						c.Println("ac")
					} else {
						c.Println("battery")
					}
					return nil
				},
			},
			&cli.Command{
				Group: "power", Name: "on-ac",
				Summary:  "Exit 0 when running on AC power, 1 otherwise",
				Examples: []string{"aos power on-ac && echo running on mains"},
				Hidden:   true,
				Run: func(c *cli.Ctx, _ []string) error {
					state, err := readPower()
					if err != nil {
						return err
					}
					if !state.OnAC {
						return &cli.ExitError{Code: 1}
					}
					return nil
				},
			},
		)

		r.Describe("battery", "Battery presence and charge")
		r.Add(
			&cli.Command{
				Group: "battery", Name: "status",
				Summary: "Print battery charge, charging state, and health (capacity, cycles) where reported",
				Args:    "[--json]",
				Examples: []string{
					"aos battery status    # 87%  discharging  3:41 remaining  (health 97%, 15 cycles, 30.7°C)",
					"aos battery status --json",
				},
				Run: runBatteryStatus,
			},
			&cli.Command{
				Group: "battery", Name: "present",
				Summary:  "Exit 0 when a battery is present, 1 otherwise",
				Examples: []string{"aos battery present && echo has a battery"},
				Run: func(c *cli.Ctx, _ []string) error {
					state, err := readPower()
					if err != nil {
						return err
					}
					if !state.HasBattery {
						return &cli.ExitError{Code: 1}
					}
					return nil
				},
			},
			&cli.Command{
				Group: "battery", Name: "percent",
				Summary:  "Print the charge percentage as a bare number",
				Examples: []string{"aos battery percent    # 87"},
				Run: func(c *cli.Ctx, _ []string) error {
					state, err := readPower()
					if err != nil {
						return err
					}
					if !state.HasBattery || state.Percent < 0 {
						return &cli.ExitError{Code: 1, Message: "no battery reading available"}
					}
					c.Printf("%d\n", state.Percent)
					return nil
				},
			},
		)
	})
}

// batteryJSON is what `battery status --json` emits.
type batteryJSON struct {
	Present   bool   `json:"present"`
	Percent   *int   `json:"percent,omitempty"`
	Status    string `json:"status,omitempty"`
	Remaining string `json:"remaining,omitempty"`
	OnAC      bool   `json:"on_ac"`
	batteryHealth
}

func runBatteryStatus(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("battery status", args)
	if err != nil {
		return err
	}
	state, err := readPower()
	if err != nil {
		return err
	}
	var health batteryHealth
	if state.HasBattery {
		health, _ = readBatteryHealth()
	}
	if asJSON {
		out := batteryJSON{Present: state.HasBattery, Status: state.Status, Remaining: state.Remaining, OnAC: state.OnAC, batteryHealth: health}
		if state.HasBattery && state.Percent >= 0 {
			out.Percent = &state.Percent
		}
		return writeJSON(c, out)
	}
	if !state.HasBattery {
		c.Println("no battery")
		return nil
	}
	line := fmt.Sprintf("%d%%", state.Percent)
	if state.Percent < 0 {
		line = "unknown"
	}
	if state.Status != "" {
		line += "  " + state.Status
	}
	if state.Remaining != "" {
		line += "  " + state.Remaining + " remaining"
	}
	if h := describeHealth(health); h != "" {
		line += "  (" + h + ")"
	}
	c.Println(line)
	return nil
}
