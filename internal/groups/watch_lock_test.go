package groups

import (
	"strings"
	"testing"
	"time"
)

func TestUntilEvent(t *testing.T) {
	for in, want := range map[string]string{"": "", "locked": "lock", "unlocked": "unlock", "sleep": "sleep", "wake": "wake"} {
		got, err := untilEvent(in)
		if err != nil || got != want {
			t.Errorf("untilEvent(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := untilEvent("asleep"); err == nil {
		t.Error("bad --until accepted")
	}
}

func names(evs []lockEvent) string {
	var n []string
	for _, e := range evs {
		n = append(n, e.Event)
	}
	return strings.Join(n, ",")
}

func TestLockTracker(t *testing.T) {
	tr := &lockTracker{interval: time.Second}
	t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
	if evs := tr.step(false, t0); len(evs) != 0 {
		t.Fatalf("baseline reported %v", evs)
	}
	if evs := tr.step(false, t0.Add(time.Second)); len(evs) != 0 {
		t.Fatalf("no change reported %v", evs)
	}
	evs := tr.step(true, t0.Add(2*time.Second))
	if names(evs) != "lock" || evs[0].State != "locked" {
		t.Fatalf("lock: %v", evs)
	}
	evs = tr.step(false, t0.Add(3*time.Second))
	if names(evs) != "unlock" || evs[0].State != "unlocked" {
		t.Fatalf("unlock: %v", evs)
	}
	// A late tick by minutes is a sleep, reported with its wake.
	evs = tr.step(false, t0.Add(3*time.Second+30*time.Minute))
	if names(evs) != "sleep,wake" || evs[0].SleptMS < 29*60*1000 {
		t.Fatalf("sleep: %+v", evs)
	}
	// Lock state changing across a sleep reports the sleep first.
	evs = tr.step(true, t0.Add(3*time.Second+60*time.Minute))
	if names(evs) != "sleep,wake,lock" {
		t.Fatalf("sleep then lock: %v", names(evs))
	}
	// A slightly late tick is just a busy process.
	if evs := tr.step(true, t0.Add(3*time.Second+60*time.Minute+3*time.Second)); len(evs) != 0 {
		t.Fatalf("jitter reported %v", evs)
	}
}

func fakeLockStates(t *testing.T, states []bool) {
	t.Helper()
	// runWatchLock probes once before looping, so the first sample is read twice.
	states = append([]bool{states[0]}, states...)
	old := screenLockedFn
	i := 0
	screenLockedFn = func() (bool, error) {
		s := states[min(i, len(states)-1)]
		i++
		return s, nil
	}
	t.Cleanup(func() { screenLockedFn = old })
}

func TestRunWatchLockUntil(t *testing.T) {
	fakeLockStates(t, []bool{false, false, true})
	c, out := pageCtx()
	if err := runWatchLock(c, []string{"--until=locked", "--interval=250ms"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"event":"lock"`) || !strings.Contains(out.String(), `"state":"locked"`) {
		t.Fatalf("until=locked: %s", out.String())
	}

	// Already in the state: exits at once.
	fakeLockStates(t, []bool{true})
	c, out = pageCtx()
	if err := runWatchLock(c, []string{"--until=locked", "--interval=250ms"}); err != nil || !strings.Contains(out.String(), `"event":"lock"`) {
		t.Fatalf("already locked: %v %s", err, out.String())
	}

	fakeLockStates(t, []bool{true, true, false})
	c, out = pageCtx()
	if err := runWatchLock(c, []string{"--until=unlocked", "--interval=250ms"}); err != nil || !strings.Contains(out.String(), `"event":"unlock"`) {
		t.Fatalf("until=unlocked: %v %s", err, out.String())
	}
}

func TestRunWatchLockStreamAndTimeout(t *testing.T) {
	fakeLockStates(t, []bool{false, true, false})
	c, out := pageCtx()
	if err := runWatchLock(c, []string{"--max=2", "--interval=250ms"}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"event":"lock"`) || !strings.Contains(lines[1], `"event":"unlock"`) || !strings.Contains(lines[1], `"seq":2`) {
		t.Fatalf("stream:\n%s", out.String())
	}

	fakeLockStates(t, []bool{false})
	c, _ = pageCtx()
	if err := runWatchLock(c, []string{"--until=locked", "--interval=250ms", "--timeout=300ms"}); exitCode(err) != exitNotFound {
		t.Fatalf("timeout: %v", err)
	}
	if err := runWatchLock(c, []string{"--until=sleep", "--max=1"}); err == nil {
		t.Fatal("--until with --max accepted")
	}
	for _, args := range [][]string{{"--until=nap"}, {"--interval=10ms"}, {"--timeout=x"}, {"--bogus"}} {
		if err := runWatchLock(c, args); err == nil {
			t.Errorf("%v accepted", args)
		}
	}
}
