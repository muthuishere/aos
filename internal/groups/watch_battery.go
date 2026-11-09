package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "watch", Name: "battery",
			Summary:  "Wait for a battery level or charging state, or print a JSON line for each power change",
			Blocking: true,
			Args:     "[--below=<pct>] [--above=<pct>] [--until=charging|discharging|full] [--interval=<5s>] [--timeout=<dur>] [--json]",
			Examples: []string{
				"aos watch battery --below=20 && echo time to save work      # exit 0 once the charge is under 20%",
				"aos watch battery --until=full --timeout=3h                 # exit 3 if it is not full in 3 hours",
				"aos watch battery --until=discharging                       # exit 0 when the charger is pulled",
				"aos watch battery                                           # a JSON line for plugged, unplugged, full and each % change",
			},
			Run: runWatchBattery,
		})
	})
}

// wbatEvent is one line of the stream, and the line printed when a condition
// is met. Condition is only set on the latter.
type wbatEvent struct {
	Event     string `json:"event"` // plugged | unplugged | full | level | met
	At        string `json:"at"`
	Percent   *int   `json:"percent,omitempty"`
	Status    string `json:"status,omitempty"`
	OnAC      bool   `json:"on_ac"`
	Condition string `json:"condition,omitempty"` // below=20, above=80, until=full
}

// wbatConditions are the exit conditions; any one being true ends the wait.
type wbatConditions struct {
	below, above int    // 0 means not set; levels are compared strictly
	until        string // charging | discharging | full, or ""
}

func (c wbatConditions) any() bool { return c.below > 0 || c.above > 0 || c.until != "" }

// wbatFull reports a battery that has finished charging. macOS says
// "charged"; Windows says nothing but 100% on mains.
func wbatFull(s powerState) bool {
	return s.Status == "charged" || (s.OnAC && s.Percent >= 100)
}

func wbatCharging(s powerState) bool {
	return s.Status == "charging" || s.Status == "finishing charge"
}

// wbatMet returns the condition that holds for s, if any. These are states,
// not edges: waiting for "below 20" while already at 15 returns at once,
// which is what an agent gating on the level wants.
func wbatMet(s powerState, c wbatConditions) (string, bool) {
	if c.below > 0 && s.Percent >= 0 && s.Percent < c.below {
		return "below=" + strconv.Itoa(c.below), true
	}
	if c.above > 0 && s.Percent >= 0 && s.Percent > c.above {
		return "above=" + strconv.Itoa(c.above), true
	}
	switch c.until {
	case "charging":
		if wbatCharging(s) {
			return "until=charging", true
		}
	case "discharging":
		if !s.OnAC {
			return "until=discharging", true
		}
	case "full":
		if wbatFull(s) {
			return "until=full", true
		}
	}
	return "", false
}

// wbatChanges lists what differs between two readings, plug state first.
func wbatChanges(prev, cur powerState) []string {
	var out []string
	if prev.OnAC != cur.OnAC {
		if cur.OnAC {
			out = append(out, "plugged")
		} else {
			out = append(out, "unplugged")
		}
	}
	if !wbatFull(prev) && wbatFull(cur) {
		out = append(out, "full")
	}
	if prev.Percent != cur.Percent && cur.Percent >= 0 {
		out = append(out, "level")
	}
	return out
}

func wbatMake(at time.Time, event string, s powerState) wbatEvent {
	e := wbatEvent{Event: event, At: at.Format(time.RFC3339), Status: s.Status, OnAC: s.OnAC}
	if s.Percent >= 0 {
		p := s.Percent
		e.Percent = &p
	}
	return e
}

type wbatWatcher struct {
	read  func() (powerState, error)
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool // false when ctx ended
	emit  func(wbatEvent)
}

type wbatResult int

const (
	wbatMetCondition wbatResult = iota
	wbatNoBattery
	wbatTimedOut
	wbatInterrupted
)

func (w *wbatWatcher) run(ctx context.Context, cond wbatConditions, interval, timeout time.Duration) (wbatResult, error) {
	begin := w.now()
	prev, err := w.read()
	if err != nil {
		return wbatMetCondition, err
	}
	if !prev.HasBattery {
		return wbatNoBattery, nil
	}
	for {
		if cond.any() {
			if why, ok := wbatMet(prev, cond); ok {
				e := wbatMake(w.now(), "met", prev)
				e.Condition = why
				w.emit(e)
				return wbatMetCondition, nil
			}
		}
		wait := interval
		if timeout > 0 {
			left := timeout - w.now().Sub(begin)
			if left <= 0 {
				return wbatTimedOut, nil
			}
			if left < wait {
				wait = left
			}
		}
		if !w.sleep(ctx, wait) {
			return wbatInterrupted, nil
		}
		cur, err := w.read()
		if err != nil || !cur.HasBattery {
			continue // one bad sample is not the end of a long wait
		}
		if !cond.any() {
			for _, name := range wbatChanges(prev, cur) {
				w.emit(wbatMake(w.now(), name, cur))
			}
		}
		prev = cur
	}
}

func runWatchBattery(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "below", "above", "until", "interval", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("below", "above", "until", "interval", "timeout", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`watch battery` takes flags only; did you mean --below=%s?", set.Rest[0])
	}
	var cond wbatConditions
	if set.Has("below") {
		if cond.below, err = wbatPercent(set, "below"); err != nil {
			return err
		}
	}
	if set.Has("above") {
		if cond.above, err = wbatPercent(set, "above"); err != nil {
			return err
		}
	}
	switch cond.until = set.String("until", ""); cond.until {
	case "", "charging", "discharging", "full":
	default:
		return fmt.Errorf("--until must be charging, discharging or full, got %q", cond.until)
	}
	interval, err := parseInterval(set.String("interval", ""), 5*time.Second)
	if err != nil {
		return err
	}
	if interval < minWatchInterval {
		return fmt.Errorf("--interval must be at least %s", minWatchInterval)
	}
	timeout, err := wbatTimeout(set.String("timeout", ""))
	if err != nil {
		return err
	}
	// The stream is always JSON lines; a met condition prints a plain line unless --json.
	asJSON := set.Has("json") || !cond.any()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	w := &wbatWatcher{
		read: readPower,
		now:  time.Now,
		sleep: func(ctx context.Context, d time.Duration) bool {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-t.C:
				return true
			}
		},
		emit: func(e wbatEvent) {
			if asJSON {
				line, _ := json.Marshal(e)
				c.Printf("%s\n", line)
				return
			}
			c.Println(wbatText(e))
		},
	}
	res, err := w.run(ctx, cond, interval, timeout)
	if err != nil {
		return err
	}
	switch res {
	case wbatNoBattery:
		return &cli.ExitError{Code: 2, Message: "this machine has no battery to watch"}
	case wbatTimedOut:
		return &cli.ExitError{Code: 3, Message: fmt.Sprintf("timed out after %s", timeout)}
	case wbatInterrupted:
		if cond.any() {
			return &cli.ExitError{Code: 130}
		}
	}
	return nil
}

func wbatPercent(set *argSet, name string) (int, error) {
	n, err := set.Int(name, 0)
	if err != nil || n < 1 || n > 100 {
		return 0, fmt.Errorf("--%s must be a percentage from 1 to 100, got %q", name, set.String(name, ""))
	}
	return n, nil
}

func wbatText(e wbatEvent) string {
	parts := []string{e.Condition}
	if e.Percent != nil {
		parts = append(parts, fmt.Sprintf("%d%%", *e.Percent))
	}
	if e.Status != "" {
		parts = append(parts, e.Status)
	}
	return strings.Join(parts, "  ")
}

// wbatTimeout reads --timeout: a Go duration, or a bare number of seconds.
// Empty means wait forever.
func wbatTimeout(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--timeout must be a positive duration like 90s or 3h, got %q", value)
	}
	return d, nil
}
