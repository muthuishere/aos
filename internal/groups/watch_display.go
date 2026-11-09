package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "watch", Name: "display",
			Summary:  "Wait for a monitor to be plugged in or removed, or print a JSON line for each",
			Blocking: true,
			Args:     "[--until=added|removed|<count>] [--interval=<2s>] [--timeout=<dur>] [--json]",
			Examples: []string{
				"aos watch display --until=added && aos window move Chrome --monitor=2   # exit 0 when a monitor appears",
				"aos watch display --until=removed --timeout=10m                         # exit 3 if none is unplugged in 10m",
				"aos watch display --until=2                                             # exit 0 once exactly 2 monitors are attached",
				"aos watch display                                                       # a JSON line per monitor added or removed",
			},
			Run: runWatchDisplay,
		})
	})
}

// wdispEvent is one monitor appearing or going away. ID is the position
// `display list` shows for it: for "added" its number now, for "removed" the
// number it had before. Name is a label built from the geometry, since the
// listing carries no product name.
type wdispEvent struct {
	Event   string `json:"event"`  // display
	Change  string `json:"change"` // added | removed
	At      string `json:"at"`
	ID      int    `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	X       *int   `json:"x,omitempty"`
	Y       *int   `json:"y,omitempty"`
	Width   int    `json:"width,omitempty"`
	Height  int    `json:"height,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	Count   int    `json:"count"` // monitors attached after the change
}

// wdispKey is what makes a monitor the same monitor between two polls. The id
// cannot be it: ids are positions sorted left to right, so unplugging the
// first monitor renumbers every other one. Geometry survives that, and a
// monitor that is rearranged or switches resolution is honestly a remove plus
// an add.
func wdispKey(m engine.Monitor) string {
	return fmt.Sprintf("%d,%d,%dx%d", m.X, m.Y, m.Width, m.Height)
}

func wdispName(m engine.Monitor) string {
	return fmt.Sprintf("%dx%d at %s%s", m.Width, m.Height, signed(m.X), signed(m.Y))
}

// wdispDiff returns removed monitors (from prev) then added ones (from cur),
// each in position order.
func wdispDiff(prev, cur []engine.Monitor, at time.Time) []wdispEvent {
	have := func(list []engine.Monitor) map[string]bool {
		set := map[string]bool{}
		for _, m := range list {
			set[wdispKey(m)] = true
		}
		return set
	}
	inPrev, inCur := have(prev), have(cur)
	var out []wdispEvent
	make1 := func(change string, m engine.Monitor) wdispEvent {
		x, y := m.X, m.Y
		return wdispEvent{
			Event: "display", Change: change, At: at.Format(time.RFC3339),
			ID: m.ID, Name: wdispName(m), X: &x, Y: &y, Width: m.Width, Height: m.Height,
			Primary: m.Primary, Count: len(cur),
		}
	}
	for _, m := range sortByID(prev) {
		if !inCur[wdispKey(m)] {
			out = append(out, make1("removed", m))
		}
	}
	for _, m := range sortByID(cur) {
		if !inPrev[wdispKey(m)] {
			out = append(out, make1("added", m))
		}
	}
	return out
}

func sortByID(list []engine.Monitor) []engine.Monitor {
	out := append([]engine.Monitor(nil), list...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// wdispUntil is the exit condition: a change, or a monitor count.
type wdispUntil struct {
	change string // added | removed
	count  int    // 0 means not set; counts start at 1 (zero monitors is not waitable)
}

func (u wdispUntil) set() bool { return u.change != "" || u.count > 0 }

type wdispWatcher struct {
	list  func() ([]engine.Monitor, error)
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) bool // false when ctx ended
	emit  func(wdispEvent)
}

type wdispResult int

const (
	wdispDone wdispResult = iota
	wdispTimedOut
	wdispInterrupted
)

func (w *wdispWatcher) run(ctx context.Context, until wdispUntil, interval, timeout time.Duration) (wdispResult, error) {
	begin := w.now()
	prev, err := w.list()
	if err != nil {
		return wdispDone, err
	}
	// A count is a state: already true means done, with a line saying so.
	if until.count > 0 && len(prev) == until.count {
		w.emit(wdispEvent{Event: "display", Change: "count", At: w.now().Format(time.RFC3339), Count: len(prev)})
		return wdispDone, nil
	}
	for {
		wait := interval
		if timeout > 0 {
			left := timeout - w.now().Sub(begin)
			if left <= 0 {
				return wdispTimedOut, nil
			}
			if left < wait {
				wait = left
			}
		}
		if !w.sleep(ctx, wait) {
			return wdispInterrupted, nil
		}
		cur, err := w.list()
		if err != nil {
			continue // a failed listing must not read as every monitor removed
		}
		events := wdispDiff(prev, cur, w.now())
		prev = cur
		for _, e := range events {
			// With --until=added, a removal is not news and stays silent.
			if until.change != "" && e.Change != until.change {
				continue
			}
			w.emit(e)
			if until.change != "" {
				return wdispDone, nil
			}
		}
		if until.count > 0 && len(cur) == until.count {
			return wdispDone, nil
		}
	}
}

func runWatchDisplay(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "until", "interval", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("until", "interval", "timeout", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`watch display` takes flags only; did you mean --until=%s?", set.Rest[0])
	}
	var until wdispUntil
	switch v := set.String("until", ""); v {
	case "":
	case "added", "removed":
		until.change = v
	default:
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fmt.Errorf("--until must be added, removed or a monitor count of 1 or more, got %q", v)
		}
		until.count = n
	}
	interval, err := parseInterval(set.String("interval", ""), 2*time.Second)
	if err != nil {
		return err
	}
	if interval < minWatchInterval {
		return fmt.Errorf("--interval must be at least %s", minWatchInterval)
	}
	timeout, err := wdispTimeout(set.String("timeout", ""))
	if err != nil {
		return err
	}
	// The stream is always JSON lines; an --until prints a plain line unless --json.
	asJSON := set.Has("json") || !until.set()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	w := &wdispWatcher{
		list: engine.ListMonitors,
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
		emit: func(e wdispEvent) {
			if asJSON {
				line, _ := json.Marshal(e)
				c.Printf("%s\n", line)
				return
			}
			c.Println(wdispText(e))
		},
	}
	res, err := w.run(ctx, until, interval, timeout)
	if err != nil {
		return err
	}
	switch res {
	case wdispTimedOut:
		return &cli.ExitError{Code: 3, Message: fmt.Sprintf("timed out after %s", timeout)}
	case wdispInterrupted:
		if until.set() {
			return &cli.ExitError{Code: 130}
		}
	}
	return nil
}

func wdispText(e wdispEvent) string {
	if e.Change == "count" {
		return fmt.Sprintf("%d attached", e.Count)
	}
	return strings.Join([]string{e.Change, strconv.Itoa(e.ID), e.Name}, "  ")
}

// wdispTimeout reads --timeout: a Go duration, or a bare number of seconds.
// Empty means wait forever.
func wdispTimeout(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--timeout must be a positive duration like 90s or 10m, got %q", value)
	}
	return d, nil
}
