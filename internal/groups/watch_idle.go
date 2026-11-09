package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "watch", Name: "idle",
			Summary:  "Exit 0 once the person has been idle for a while, or as soon as they are active again",
			Blocking: true,
			Args:     "(--for=<duration> | --active) [--interval=<1s>] [--timeout=<duration>] [--json]",
			Examples: []string{
				"aos watch idle --for=10m && echo \"away from the desk\"",
				"aos watch idle --active --timeout=1h   # block until the next key press or mouse move",
				"aos watch idle --for=5m --json",
				"# idle time is input idle: macOS HIDIdleTime, Windows GetLastInputInfo, Linux xprintidle (X11 only)",
				"# exit 0 when met, 3 on --timeout, 1 when this machine cannot report idle time",
			},
			Run: runWatchIdle,
		})
	})
}

const (
	defaultIdleWatchInterval = time.Second
	minIdleWatchInterval     = 250 * time.Millisecond
)

// idleWatch is what a `watch idle` waits for: idle >= threshold (for), or an
// input after the baseline sample (active).
type idleWatch struct {
	threshold time.Duration // --for
	active    bool          // --active
	started   bool
	prev      time.Duration
}

// parseIdleWatch validates the flags: exactly one of --for and --active.
func parseIdleWatch(forValue string, active bool) (*idleWatch, error) {
	switch {
	case forValue != "" && active:
		return nil, fmt.Errorf("use --for or --active, not both")
	case forValue == "" && !active:
		return nil, fmt.Errorf("give --for=<duration> (wait until idle that long) or --active (wait for the next input)")
	case active:
		return &idleWatch{active: true}, nil
	}
	d, err := parseInterval(forValue, 0)
	if err != nil || d <= 0 {
		return nil, fmt.Errorf("--for must be a duration like 30s or 10m, got %q", forValue)
	}
	return &idleWatch{threshold: d}, nil
}

// observe takes one idle sample and reports whether the condition now holds.
// --active compares each sample with the one before: the idle counter only
// ever grows until an input resets it, so a drop means the person is back.
func (w *idleWatch) observe(idle time.Duration) bool {
	defer func() { w.started, w.prev = true, idle }()
	if w.active {
		return w.started && idle < w.prev
	}
	return idle >= w.threshold
}

// idleEvent is the JSON line printed when the condition is met.
type idleEvent struct {
	Event  string `json:"event"`
	At     string `json:"at"`
	Seq    int    `json:"seq"`
	State  string `json:"state"` // idle | active
	IdleMS int64  `json:"idle_ms"`
}

func runWatchIdle(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "for", "interval", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("for", "active", "interval", "timeout", "json"); err != nil {
		return err
	}
	w, err := parseIdleWatch(set.String("for", ""), set.Has("active"))
	if err != nil {
		return err
	}
	interval, err := parseInterval(set.String("interval", ""), defaultIdleWatchInterval)
	if err != nil {
		return err
	}
	if interval < minIdleWatchInterval {
		return fmt.Errorf("--interval must be at least %s", minIdleWatchInterval)
	}
	var timeout time.Duration
	if v := set.String("timeout", ""); v != "" {
		if timeout, err = parseInterval(v, 0); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	if _, ok := idleFn(); !ok {
		return fmt.Errorf("this machine cannot report input idle time (on Linux it needs xprintidle on X11)")
	}
	return idleWatchLoop(c, w, interval, timeout, set.Has("json"))
}

func idleWatchLoop(c *cli.Ctx, w *idleWatch, interval, timeout time.Duration, asJSON bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		if idle, ok := idleFn(); !ok {
			c.Warnf("aos: idle time unavailable this tick\n")
		} else if w.observe(idle) {
			state := "idle"
			if w.active {
				state = "active"
			}
			ev := idleEvent{Event: "idle", At: time.Now().Format(time.RFC3339), Seq: 1, State: state, IdleMS: idle.Milliseconds()}
			if asJSON {
				line, err := json.Marshal(ev)
				if err != nil {
					return err
				}
				c.Printf("%s\n", line)
			} else if w.active {
				c.Printf("active again (idle %s)\n", idle.Round(time.Millisecond))
			} else {
				c.Printf("idle for %s\n", idle.Round(time.Second))
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			what := "the person did not become idle for " + w.threshold.String()
			if w.active {
				what = "no input"
			}
			return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%s within %s", what, timeout)}
		case <-ticker.C:
		}
	}
}
