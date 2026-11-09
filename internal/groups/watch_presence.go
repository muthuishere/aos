package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// watch camera and watch mic report whether some app is using the camera or
// the microphone right now. That is the signal for "the person is in a
// meeting": an agent waits on it before starting noisy work (a build, speech,
// a sound) and carries on when the call is over.
//
//	aos watch camera --until=free && <the agent starts the noisy work>
//
// The OS answers "is it in use by anyone" without opening the device, so the
// watcher never lights the privacy indicator or raises a permission prompt.

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "watch", Name: "camera",
				Summary:  "Report when any app starts or stops using the camera",
				Blocking: true,
				Args:     "[--until=busy|free] [--interval=<1s>] [--timeout=<duration>] [--json]",
				Examples: []string{
					"aos watch camera",
					"aos watch camera --until=busy --timeout=10m",
					"aos watch camera --until=free && echo 'call is over, start the build'",
					"aos watch camera --json",
					"# with --until: exit 0 when the state is reached (at once if it already is), 3 on --timeout",
				},
				Platforms: []string{"darwin", "windows"},
				Run:       func(c *cli.Ctx, args []string) error { return runWatchPresence(c, "camera", readCameraUse, args) },
			},
			&cli.Command{
				Group: "watch", Name: "mic",
				Summary:  "Report when any app starts or stops using the microphone",
				Blocking: true,
				Args:     "[--until=busy|free] [--interval=<1s>] [--timeout=<duration>] [--json]",
				Examples: []string{
					"aos watch mic",
					"aos watch mic --until=free --timeout=1h",
					"aos watch mic --until=busy && echo 'someone is on a call'",
					"aos watch mic --json",
					"# with --until: exit 0 when the state is reached (at once if it already is), 3 on --timeout",
				},
				Platforms: []string{"darwin", "windows"},
				Run:       func(c *cli.Ctx, args []string) error { return runWatchPresence(c, "mic", readMicUse, args) },
			},
		)
	})
}

// deviceUse is what the OS says about one capture device class at one instant.
// Apps is filled only where the OS names the user (Windows for both, macOS for
// the microphone); Devices names the hardware that is running.
type deviceUse struct {
	Busy    bool
	Apps    []string
	Devices []string
}

// presenceEvent is one line of --json output.
type presenceEvent struct {
	Event   string   `json:"event"` // camera | mic
	State   string   `json:"state"` // busy | free
	Apps    []string `json:"apps,omitempty"`
	Devices []string `json:"devices,omitempty"`
	At      string   `json:"at"`
	Seq     int      `json:"seq"`
}

func (u deviceUse) state() string {
	if u.Busy {
		return "busy"
	}
	return "free"
}

// normalizeUse sorts and de-duplicates the names so two samples of the same
// situation compare equal and print the same way.
func normalizeUse(u deviceUse) deviceUse {
	u.Apps, u.Devices = uniqSorted(u.Apps), uniqSorted(u.Devices)
	return u
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// presenceOpts is everything the loop needs, so tests drive it without flags.
type presenceOpts struct {
	Kind     string        // camera | mic
	Until    string        // "", busy or free
	Interval time.Duration // poll period
	Timeout  time.Duration // 0 means none
	JSON     bool
	Now      func() time.Time
}

func runWatchPresence(c *cli.Ctx, kind string, probe func() (deviceUse, error), args []string) error {
	set, err := parseArgs(args, "until", "interval", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("until", "interval", "timeout", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`watch %s` takes no positional arguments", kind)
	}
	o := presenceOpts{Kind: kind, Until: set.String("until", ""), JSON: set.Has("json"), Now: time.Now}
	if o.Until != "" && o.Until != "busy" && o.Until != "free" {
		return fmt.Errorf("--until must be busy or free, got %q", o.Until)
	}
	if o.Interval, err = parseInterval(set.String("interval", ""), time.Second); err != nil {
		return err
	}
	if o.Interval < 100*time.Millisecond {
		return fmt.Errorf("--interval must be at least 100ms")
	}
	if v := set.String("timeout", ""); v != "" {
		if o.Timeout, err = parseInterval(v, 0); err != nil || o.Timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}

	// SIGTERM too: agents end a watch that way.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return watchPresence(ctx, c, o, probe)
}

// watchPresence samples the probe until the condition is met. Without --until
// it is a stream: the first line is the current state, then one line per
// change. With --until it prints the line that satisfies the condition and
// returns nil; --timeout is exit 3 only there, because a plain stream has no
// condition to miss.
func watchPresence(ctx context.Context, c *cli.Ctx, o presenceOpts, probe func() (deviceUse, error)) error {
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()

	var last deviceUse
	started := false
	seq := 0
	for {
		use, err := probe()
		switch {
		case err != nil && !started:
			return err // the first sample failing is a broken command, not a hiccup
		case err != nil:
			c.Warnf("aos: %v\n", err)
		default:
			use = normalizeUse(use)
			changed := !started || use.state() != last.state() || strings.Join(use.Apps, "\x00") != strings.Join(last.Apps, "\x00")
			started, last = true, use
			if changed {
				met := o.Until != "" && use.state() == o.Until
				if o.Until == "" || met {
					seq++
					if err := printPresence(c, o, use, seq); err != nil {
						return err
					}
				}
				if met {
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			timedOut := ctx.Err() == context.DeadlineExceeded
			switch {
			case o.Until == "":
				return nil
			case timedOut:
				return &cli.ExitError{Code: 3, Message: fmt.Sprintf("timed out after %s waiting for the %s to be %s", o.Timeout, o.Kind, o.Until)}
			default:
				// Interrupted before the condition: not success, so a `&&`
				// chain must not carry on.
				return &cli.ExitError{Code: 130, Message: "interrupted"}
			}
		case <-ticker.C:
		}
	}
}

func printPresence(c *cli.Ctx, o presenceOpts, u deviceUse, seq int) error {
	if o.JSON {
		line, err := json.Marshal(presenceEvent{
			Event: o.Kind, State: u.state(), Apps: u.Apps, Devices: u.Devices,
			At: o.Now().Format(time.RFC3339), Seq: seq,
		})
		if err != nil {
			return err
		}
		c.Printf("%s\n", line)
		return nil
	}
	text := o.Kind + " " + u.state()
	if u.Busy {
		who := u.Apps
		if len(who) == 0 {
			who = u.Devices
		}
		if len(who) > 0 {
			text += " (" + strings.Join(who, ", ") + ")"
		}
	}
	c.Println(text)
	return nil
}
