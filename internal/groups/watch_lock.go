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
			Group: "watch", Name: "lock",
			Summary:  "Print a JSON line when the screen locks or unlocks and when the machine sleeps or wakes",
			Blocking: true,
			Args:     "[--until=locked|unlocked|sleep|wake] [--interval=<1s>] [--timeout=<duration>] [--max=<n>]",
			Examples: []string{
				"aos watch lock",
				"aos watch lock --until=unlocked --timeout=2h && echo \"the person is back\"",
				"aos watch lock --until=locked",
				"aos watch lock --until=wake --timeout=8h",
				"# lock: macOS CGSession, Windows WTS session state. sleep: seen as a gap in the poll clock, so sleep and wake are reported together on resume (sleep carries slept_ms)",
				"# --until exits 0 when that state is reached (at once if already locked/unlocked), 3 on --timeout",
			},
			Run: runWatchLock,
		})
	})
}

const (
	defaultLockWatchInterval = time.Second
	minLockWatchInterval     = 250 * time.Millisecond
)

// lockEvent is the one line a lock watch prints.
type lockEvent struct {
	Event   string `json:"event"` // lock | unlock | sleep | wake
	At      string `json:"at"`
	Seq     int    `json:"seq"`
	State   string `json:"state,omitempty"` // locked | unlocked, after the event
	SleptMS int64  `json:"slept_ms,omitempty"`
}

// lockTracker turns polled samples into events. The first sample is the state
// the caller was already in, so it only sets the baseline.
type lockTracker struct {
	interval time.Duration
	started  bool
	locked   bool
	last     time.Time
}

// sleepGap is how late a poll must be before the machine is taken to have
// slept: a process that is merely busy is late by milliseconds, a suspended
// machine by minutes. Wall-clock time is compared because the monotonic clock
// stops during sleep on macOS.
func (t *lockTracker) sleepGap() time.Duration {
	return max(3*t.interval, 5*time.Second)
}

// step takes one sample (locked, at now) and returns the events it shows.
func (t *lockTracker) step(locked bool, now time.Time) []lockEvent {
	now = now.Round(0) // drop the monotonic reading: compare wall clocks
	var out []lockEvent
	if t.started {
		if gap := now.Sub(t.last); gap > t.sleepGap() {
			slept := gap - t.interval
			out = append(out,
				lockEvent{Event: "sleep", At: t.last.Format(time.RFC3339), SleptMS: slept.Milliseconds()},
				lockEvent{Event: "wake", At: now.Format(time.RFC3339), SleptMS: slept.Milliseconds()})
		}
		if locked != t.locked {
			name := "unlock"
			if locked {
				name = "lock"
			}
			out = append(out, lockEvent{Event: name, At: now.Format(time.RFC3339)})
		}
	}
	t.started, t.locked, t.last = true, locked, now
	for i := range out {
		out[i].State = lockStateName(t.locked)
	}
	return out
}

// screenLockedFn is a seam for tests.
var screenLockedFn = screenLocked

func lockStateName(locked bool) string {
	if locked {
		return "locked"
	}
	return "unlocked"
}

// untilEvent maps an --until state to the event that reaches it.
func untilEvent(until string) (string, error) {
	switch until {
	case "":
		return "", nil
	case "locked":
		return "lock", nil
	case "unlocked":
		return "unlock", nil
	case "sleep", "wake":
		return until, nil
	}
	return "", fmt.Errorf("--until must be locked, unlocked, sleep or wake, got %q", until)
}

// satisfiedAtStart: --until=locked / unlocked are states, so they hold already
// if the machine is in them. Sleep and wake are moments and cannot.
func satisfiedAtStart(until string, locked bool) bool {
	return (until == "locked" && locked) || (until == "unlocked" && !locked)
}

func runWatchLock(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "until", "interval", "timeout", "max")
	if err != nil {
		return err
	}
	if err := set.Reject("until", "interval", "timeout", "max"); err != nil {
		return err
	}
	until := set.String("until", "")
	want, err := untilEvent(until)
	if err != nil {
		return err
	}
	interval, err := parseInterval(set.String("interval", ""), defaultLockWatchInterval)
	if err != nil {
		return err
	}
	if interval < minLockWatchInterval {
		return fmt.Errorf("--interval must be at least %s", minLockWatchInterval)
	}
	var timeout time.Duration
	if v := set.String("timeout", ""); v != "" {
		if timeout, err = parseInterval(v, 0); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	max, err := set.Int("max", 0)
	if err != nil || max < 0 {
		return fmt.Errorf("--max must be a non-negative number")
	}
	if want != "" && max > 0 {
		return fmt.Errorf("use --until or --max, not both")
	}
	if _, err := screenLockedFn(); err != nil {
		return err
	}
	return lockWatchLoop(c, &lockTracker{interval: interval}, until, want, interval, timeout, max)
}

func lockWatchLoop(c *cli.Ctx, tr *lockTracker, until, want string, interval, timeout time.Duration, max int) error {
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

	seq := 0
	emit := func(ev lockEvent) error {
		seq++
		ev.Seq = seq
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		c.Printf("%s\n", line)
		return nil
	}
	for {
		locked, err := screenLockedFn()
		if err != nil {
			c.Warnf("aos: %v\n", err)
		} else {
			first := !tr.started
			events := tr.step(locked, time.Now())
			if first && satisfiedAtStart(until, locked) {
				name := "lock"
				if !locked {
					name = "unlock"
				}
				return emit(lockEvent{Event: name, At: time.Now().Format(time.RFC3339), State: lockStateName(locked)})
			}
			for _, ev := range events {
				if err := emit(ev); err != nil {
					return err
				}
				if (want != "" && ev.Event == want) || (max > 0 && seq >= max) {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			if until == "" {
				return nil
			}
			return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("never reached %q within %s", until, timeout)}
		case <-ticker.C:
		}
	}
}
