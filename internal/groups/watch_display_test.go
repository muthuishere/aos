package groups

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/engine"
)

func mon(id, x, y, w, h int) engine.Monitor {
	return engine.Monitor{ID: id, X: x, Y: y, Width: w, Height: h, Primary: x == 0 && y == 0}
}

var (
	left  = mon(1, 0, 0, 1728, 1117)
	right = mon(2, 1728, 0, 2560, 1440)
)

func TestWdispDiffAdded(t *testing.T) {
	events := wdispDiff([]engine.Monitor{left}, []engine.Monitor{left, right}, time.Time{})
	if len(events) != 1 {
		t.Fatalf("events %+v", events)
	}
	e := events[0]
	if e.Change != "added" || e.ID != 2 || e.Name != "2560x1440 at +1728+0" || e.Count != 2 || e.Primary {
		t.Fatalf("event %+v", e)
	}
}

func TestWdispDiffSurvivesRenumbering(t *testing.T) {
	// Unplugging the left monitor makes the right one id 1. It is still the
	// same monitor, so only the left one is reported, under its old id.
	renumbered := right
	renumbered.ID = 1
	events := wdispDiff([]engine.Monitor{left, right}, []engine.Monitor{renumbered}, time.Time{})
	if len(events) != 1 || events[0].Change != "removed" || events[0].ID != 1 || events[0].Width != 1728 || events[0].Count != 1 {
		t.Fatalf("events %+v", events)
	}
}

func TestWdispDiffNothingAndOrder(t *testing.T) {
	if got := wdispDiff([]engine.Monitor{left, right}, []engine.Monitor{left, right}, time.Time{}); got != nil {
		t.Fatalf("got %+v", got)
	}
	other := mon(2, 0, 1117, 1920, 1080)
	events := wdispDiff([]engine.Monitor{left, right}, []engine.Monitor{left, other}, time.Time{})
	var got []string
	for _, e := range events {
		got = append(got, e.Change)
	}
	if !reflect.DeepEqual(got, []string{"removed", "added"}) {
		t.Fatalf("order %v", got)
	}
}

type wdispScript struct {
	lists  [][]engine.Monitor
	errs   map[int]error
	next   int
	clock  time.Time
	events []wdispEvent
}

func (s *wdispScript) watcher() *wdispWatcher {
	return &wdispWatcher{
		list: func() ([]engine.Monitor, error) {
			i := s.next
			s.next++
			if err := s.errs[i]; err != nil {
				return nil, err
			}
			if i >= len(s.lists) {
				i = len(s.lists) - 1
			}
			return s.lists[i], nil
		},
		now: func() time.Time { return s.clock },
		sleep: func(_ context.Context, d time.Duration) bool {
			s.clock = s.clock.Add(d)
			return true
		},
		emit: func(e wdispEvent) { s.events = append(s.events, e) },
	}
}

func list(ms ...engine.Monitor) []engine.Monitor { return ms }

func TestWatchDisplayUntilAddedIgnoresRemovalsAndBaseline(t *testing.T) {
	s := &wdispScript{lists: [][]engine.Monitor{list(left, right), list(left), list(left, right)}}
	res, _ := s.watcher().run(context.Background(), wdispUntil{change: "added"}, 2*time.Second, 0)
	if res != wdispDone || len(s.events) != 1 || s.events[0].Change != "added" || s.events[0].ID != 2 {
		t.Fatalf("res %v events %+v", res, s.events)
	}
}

func TestWatchDisplayUntilRemoved(t *testing.T) {
	s := &wdispScript{lists: [][]engine.Monitor{list(left, right), list(left, right), list(left)}}
	res, _ := s.watcher().run(context.Background(), wdispUntil{change: "removed"}, time.Second, 0)
	if res != wdispDone || len(s.events) != 1 || s.events[0].Change != "removed" || s.events[0].ID != 2 {
		t.Fatalf("res %v events %+v", res, s.events)
	}
}

func TestWatchDisplayUntilCount(t *testing.T) {
	s := &wdispScript{lists: [][]engine.Monitor{list(left), list(left), list(left, right)}}
	res, _ := s.watcher().run(context.Background(), wdispUntil{count: 2}, time.Second, 0)
	if res != wdispDone || s.clock.Sub(time.Time{}) != 2*time.Second {
		t.Fatalf("res %v after %v", res, s.clock.Sub(time.Time{}))
	}
	// Already at the count: returns at once, with a line saying so.
	s = &wdispScript{lists: [][]engine.Monitor{list(left, right)}}
	res, _ = s.watcher().run(context.Background(), wdispUntil{count: 2}, time.Second, 0)
	if res != wdispDone || !s.clock.IsZero() || len(s.events) != 1 || s.events[0].Change != "count" || s.events[0].Count != 2 {
		t.Fatalf("res %v events %+v", res, s.events)
	}
}

func TestWatchDisplayFlakyListingIsNotRemoval(t *testing.T) {
	s := &wdispScript{
		lists: [][]engine.Monitor{list(left, right), list(left, right), list(left, right)},
		errs:  map[int]error{1: errors.New("display server busy")},
	}
	w := s.watcher()
	calls := 0
	inner := w.sleep
	w.sleep = func(ctx context.Context, d time.Duration) bool {
		calls++
		return calls <= 3 && inner(ctx, d)
	}
	res, _ := w.run(context.Background(), wdispUntil{}, time.Second, 0)
	if res != wdispInterrupted || len(s.events) != 0 {
		t.Fatalf("res %v events %+v", res, s.events)
	}
}

func TestWatchDisplayTimeoutAndStream(t *testing.T) {
	s := &wdispScript{lists: [][]engine.Monitor{list(left)}}
	res, _ := s.watcher().run(context.Background(), wdispUntil{change: "added"}, 2*time.Second, 5*time.Second)
	if res != wdispTimedOut || s.clock.Sub(time.Time{}) != 5*time.Second {
		t.Fatalf("res %v slept %v", res, s.clock.Sub(time.Time{}))
	}
	// Stream mode reports both directions.
	s = &wdispScript{lists: [][]engine.Monitor{list(left), list(left, right), list(left)}}
	w := s.watcher()
	calls := 0
	inner := w.sleep
	w.sleep = func(ctx context.Context, d time.Duration) bool {
		calls++
		return calls <= 2 && inner(ctx, d)
	}
	w.run(context.Background(), wdispUntil{}, time.Second, 0)
	if len(s.events) != 2 || s.events[0].Change != "added" || s.events[1].Change != "removed" {
		t.Fatalf("events %+v", s.events)
	}
}

func TestWdispTimeout(t *testing.T) {
	if d, err := wdispTimeout("10m"); err != nil || d != 10*time.Minute {
		t.Fatalf("%v %v", d, err)
	}
	if _, err := wdispTimeout("-1s"); err == nil {
		t.Fatal("negative should fail")
	}
}
