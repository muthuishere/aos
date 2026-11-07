package groups

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// focusState is what `focus status --json` emits.
type focusState struct {
	On bool `json:"on"`
	// Mode names the Focus on macOS ("do-not-disturb", "work", ...), "" elsewhere.
	Mode string `json:"mode,omitempty"`
	// Until is when it ends by itself, "" for open-ended.
	Until string `json:"until,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("focus", "Do Not Disturb / Focus: read it, and set it where the OS allows")
		r.Add(
			&cli.Command{
				Group: "focus", Name: "status",
				Summary: "Print whether Do Not Disturb (Focus) is on, which mode, and until when",
				Args:    "[--json]",
				Examples: []string{
					"aos focus status",
					"aos focus status --json",
				},
				Run: runFocusStatus,
			},
			&cli.Command{
				Group: "focus", Name: "on",
				Summary: "Turn Do Not Disturb on, optionally for a while (GNOME)",
				Args:    "[--for=<duration>]",
				Examples: []string{
					"aos focus on",
					"aos focus on --for=1h",
				},
				Run: runFocusOn,
			},
			&cli.Command{
				Group: "focus", Name: "off",
				Summary:  "Turn Do Not Disturb off (GNOME)",
				Examples: []string{"aos focus off"},
				Run:      runFocusOff,
			},
		)
	})
}

func runFocusStatus(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("focus status", args)
	if err != nil {
		return err
	}
	st, err := readFocus()
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(c, st)
	}
	c.Println(describeFocus(st))
	return nil
}

func runFocusOn(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "for")
	if err != nil {
		return err
	}
	if err := set.Reject("for"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("unexpected argument %q", set.Rest[0])
	}
	d, err := parseFocusFor(set.String("for", ""))
	if err != nil {
		return err
	}
	if err := setFocus(true, d); err != nil {
		return err
	}
	if d > 0 {
		c.Printf("do not disturb on for %s\n", d)
	} else {
		c.Println("do not disturb on")
	}
	return nil
}

func runFocusOff(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`focus off` takes no arguments")
	}
	if err := setFocus(false, 0); err != nil {
		return err
	}
	c.Println("do not disturb off")
	return nil
}

func describeFocus(st focusState) string {
	if !st.On {
		return "off"
	}
	line := "on"
	if st.Mode != "" {
		line += "  " + st.Mode
	}
	if st.Until != "" {
		line += "  until " + st.Until
	}
	return line
}

func parseFocusFor(s string) (time.Duration, error) {
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Minute {
		return 0, fmt.Errorf("--for wants a duration of a minute or more, like 30m or 1h, got %q", s)
	}
	return d, nil
}

// focusUnsupported is the answer where the OS has no supported way to set it.
func focusUnsupported(what string) error {
	return &cli.ExitError{Code: 2, Message: what}
}

// appleEpoch is the Core Data reference date the Focus store counts from.
var appleEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

// parseFocusAssertions reads macOS ~/Library/DoNotDisturb/DB/Assertions.json.
// A manual Focus is an assertion record; ending it adds an invalidation
// naming the same UUID. Scheduled and automation-triggered Focus modes are
// not in this file, so they read as off.
func parseFocusAssertions(data []byte, now time.Time) (focusState, error) {
	var doc struct {
		Data []struct {
			Assertions []struct {
				UUID    string `json:"assertionUUID"`
				Details struct {
					Mode     string `json:"assertionDetailsModeIdentifier"`
					Lifetime struct {
						Type string  `json:"assertionDetailsLifetimeType"`
						End  float64 `json:"assertionDetailsDateIntervalLifetimeEndDateTimestamp"`
					} `json:"assertionDetailsLifetime"`
				} `json:"assertionDetails"`
			} `json:"storeAssertionRecords"`
			Invalidations []struct {
				Assertion struct {
					UUID string `json:"assertionUUID"`
				} `json:"invalidationAssertion"`
			} `json:"storeInvalidationRecords"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return focusState{}, err
	}
	for _, d := range doc.Data {
		ended := map[string]bool{}
		for _, inv := range d.Invalidations {
			ended[inv.Assertion.UUID] = true
		}
		for _, a := range d.Assertions {
			if ended[a.UUID] {
				continue
			}
			st := focusState{On: true, Mode: focusModeName(a.Details.Mode)}
			if a.Details.Lifetime.End > 0 {
				end := appleEpoch.Add(time.Duration(a.Details.Lifetime.End * float64(time.Second)))
				if !end.After(now) {
					continue
				}
				st.Until = end.Local().Format(time.RFC3339)
			}
			return st, nil
		}
	}
	return focusState{}, nil
}

// gnomeBanners is the show-banners value for a DND state (banners off = DND on).
func gnomeBanners(dndOn bool) string {
	if dndOn {
		return "false"
	}
	return "true"
}

// focusRestoreScript turns GNOME banners back on after d.
func focusRestoreScript(d time.Duration) string {
	return fmt.Sprintf("sleep %d; gsettings set org.gnome.desktop.notifications show-banners true", int(d.Seconds()))
}

// focusModeName turns "com.apple.donotdisturb.mode.default" into
// "do-not-disturb" and "com.apple.focus.work" into "work".
func focusModeName(id string) string {
	if id == "" || strings.HasSuffix(id, ".default") {
		return "do-not-disturb"
	}
	return id[strings.LastIndex(id, ".")+1:]
}
