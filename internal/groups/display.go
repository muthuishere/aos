package groups

import (
	"fmt"
	"strconv"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("display", "Attached monitors")
		r.Add(
			&cli.Command{
				Group: "display", Name: "list",
				Summary:  "List attached monitors with geometry and focus state",
				Aliases:  []string{"monitors"},
				Examples: []string{"aos display list"},
				Run:      runDisplayList,
			},
			&cli.Command{
				Group: "display", Name: "active",
				Summary:  "Print the ID of the monitor the pointer is on",
				Examples: []string{"aos capture screenshot --monitor=$(aos display active)"},
				Run: func(c *cli.Ctx, _ []string) error {
					return printMonitorID(c, func(m engine.Monitor) bool { return m.Active })
				},
			},
			&cli.Command{
				Group: "display", Name: "focused",
				Summary:  "Print the ID of the monitor holding the frontmost window",
				Examples: []string{"aos window move Chrome --monitor=$(aos display focused)"},
				Run: func(c *cli.Ctx, _ []string) error {
					return printMonitorID(c, func(m engine.Monitor) bool { return m.Focused })
				},
			},
		)
	})
}

func runDisplayList(c *cli.Ctx, _ []string) error {
	monitors, err := engine.ListMonitors()
	if err != nil {
		return err
	}
	if len(monitors) == 0 {
		c.Println("no monitors detected")
		return nil
	}
	for _, m := range monitors {
		c.Printf("%d  %dx%d%s%s%s\n",
			m.ID, m.Width, m.Height, signed(m.X), signed(m.Y), monitorTags(m))
	}
	return nil
}

// signed renders a coordinate with an explicit sign, so a monitor placed to the
// left of the primary reads as 1728x1117-1728+0 rather than "+-1728".
func signed(value int) string {
	if value < 0 {
		return strconv.Itoa(value)
	}
	return "+" + strconv.Itoa(value)
}

// monitorTags renders the flags that distinguish otherwise identical monitors:
// where the pointer is (active) versus where the frontmost window is (focused).
func monitorTags(m engine.Monitor) string {
	var tags []string
	if m.Primary {
		tags = append(tags, "primary")
	}
	if m.Active {
		tags = append(tags, "active")
	}
	if m.Focused {
		tags = append(tags, "focused")
	}
	if len(tags) == 0 {
		return ""
	}
	return "  " + fmt.Sprint(tags)
}

func printMonitorID(c *cli.Ctx, want func(engine.Monitor) bool) error {
	monitors, err := engine.ListMonitors()
	if err != nil {
		return err
	}
	for _, m := range monitors {
		if want(m) {
			c.Printf("%d\n", m.ID)
			return nil
		}
	}
	return &cli.ExitError{Code: 1, Message: "no monitor matched"}
}
