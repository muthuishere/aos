package groups

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// wprocScript drives a watcher with one process listing per tick and a fake
// clock that advances by the requested sleep.
type wprocScript struct {
	ticks  [][]processInfo
	next   int
	clock  time.Time
	events []wprocEvent
	codes  map[int]int
	held   []int
}

func (s *wprocScript) watcher() *wprocWatcher {
	return &wprocWatcher{
		list: func() ([]processInfo, error) {
			i := s.next
			if i >= len(s.ticks) {
				i = len(s.ticks) - 1
			}
			s.next++
			return s.ticks[i], nil
		},
		hold: func(pid int) { s.held = append(s.held, pid) },
		exitCode: func(pid int) (int, bool) {
			code, ok := s.codes[pid]
			return code, ok
		},
		now: func() time.Time { return s.clock },
		sleep: func(_ context.Context, d time.Duration) bool {
			s.clock = s.clock.Add(d)
			return true
		},
		emit: func(e wprocEvent) { s.events = append(s.events, e) },
	}
}

func procs(pairs ...any) []processInfo {
	var out []processInfo
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, processInfo{PID: pairs[i].(int), Name: pairs[i+1].(string)})
	}
	return out
}

func TestWprocSelectIsExactAndCaseInsensitive(t *testing.T) {
	all := procs(1, "sleep", 2, "sleepd", 3, "SLEEP")
	got := wprocSelect(all, "sleep", 0)
	if !reflect.DeepEqual(got, wprocSnapshot{1: "sleep", 3: "SLEEP"}) {
		t.Fatalf("got %v", got)
	}
	if got := wprocSelect(all, "", 2); !reflect.DeepEqual(got, wprocSnapshot{2: "sleepd"}) {
		t.Fatalf("by pid got %v", got)
	}
}

func TestWprocDiff(t *testing.T) {
	started, stopped := wprocDiff(wprocSnapshot{1: "a", 2: "a"}, wprocSnapshot{2: "a", 5: "a", 4: "a"})
	if !reflect.DeepEqual(started, []int{4, 5}) || !reflect.DeepEqual(stopped, []int{1}) {
		t.Fatalf("started %v stopped %v", started, stopped)
	}
}

func TestWatchProcessExit(t *testing.T) {
	s := &wprocScript{
		ticks: [][]processInfo{procs(7, "ffmpeg"), procs(7, "ffmpeg"), procs()},
		codes: map[int]int{7: 0},
	}
	res, err := s.watcher().run(context.Background(), "ffmpeg", 0, wprocUntilExit, time.Second, 0)
	if err != nil || res != wprocDone {
		t.Fatalf("res %v err %v", res, err)
	}
	zero := 0
	want := []wprocEvent{{Event: "stop", At: s.clock.Format(time.RFC3339), PID: 7, Name: "ffmpeg", ExitCode: &zero}}
	if !reflect.DeepEqual(s.events, want) {
		t.Fatalf("events %+v", s.events)
	}
	if !reflect.DeepEqual(s.held, []int{7}) {
		t.Fatalf("held %v", s.held)
	}
}

func TestWatchProcessExitWhenNotRunning(t *testing.T) {
	s := &wprocScript{ticks: [][]processInfo{procs()}}
	res, _ := s.watcher().run(context.Background(), "ffmpeg", 0, wprocUntilExit, time.Second, 0)
	if res != wprocNotRunning {
		t.Fatalf("res %v", res)
	}
}

func TestWatchProcessExitIgnoresNewcomers(t *testing.T) {
	// pid 8 starts after the watch began; only pid 7 ending must finish it.
	s := &wprocScript{ticks: [][]processInfo{procs(7, "x"), procs(7, "x", 8, "x"), procs(8, "x")}}
	res, _ := s.watcher().run(context.Background(), "x", 0, wprocUntilExit, time.Second, 0)
	if res != wprocDone || len(s.events) != 1 || s.events[0].PID != 7 || s.events[0].ExitCode != nil {
		t.Fatalf("res %v events %+v", res, s.events)
	}
}

func TestWatchProcessStart(t *testing.T) {
	// Already running at the start does not count; a new pid does.
	s := &wprocScript{ticks: [][]processInfo{procs(1, "Xcode"), procs(1, "Xcode"), procs(1, "Xcode", 9, "Xcode")}}
	res, _ := s.watcher().run(context.Background(), "Xcode", 0, wprocUntilStart, time.Second, 0)
	if res != wprocDone || len(s.events) != 1 || s.events[0].Event != "start" || s.events[0].PID != 9 {
		t.Fatalf("res %v events %+v", res, s.events)
	}
}

func TestWatchProcessTimeout(t *testing.T) {
	s := &wprocScript{ticks: [][]processInfo{procs(7, "x")}}
	res, _ := s.watcher().run(context.Background(), "x", 0, wprocUntilExit, time.Second, 2500*time.Millisecond)
	if res != wprocTimedOut {
		t.Fatalf("res %v", res)
	}
	if got := s.clock.Sub(time.Time{}); got != 2500*time.Millisecond {
		t.Fatalf("slept %v, want exactly the timeout", got)
	}
}

func TestWatchProcessStream(t *testing.T) {
	s := &wprocScript{ticks: [][]processInfo{procs(1, "n"), procs(1, "n", 2, "n"), procs(2, "n")}}
	w := s.watcher()
	calls := 0
	inner := w.sleep
	w.sleep = func(ctx context.Context, d time.Duration) bool {
		calls++
		return calls <= 2 && inner(ctx, d)
	}
	res, _ := w.run(context.Background(), "n", 0, wprocStream, time.Second, 0)
	if res != wprocInterrupted {
		t.Fatalf("res %v", res)
	}
	var got []string
	for _, e := range s.events {
		got = append(got, e.Event+":"+string(rune('0'+e.PID)))
	}
	if !reflect.DeepEqual(got, []string{"start:1", "start:2", "stop:1"}) {
		t.Fatalf("events %v", got)
	}
}

func TestWprocTimeout(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "30": 30 * time.Second, "90s": 90 * time.Second, "5m": 5 * time.Minute} {
		if got, err := wprocTimeout(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"abc", "-5s", "0s"} {
		if _, err := wprocTimeout(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}
