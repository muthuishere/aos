package groups

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseAwakeFor(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "2h": 2 * time.Hour, "1h30m": 90 * time.Minute} {
		got, err := parseAwakeFor(in)
		if err != nil || got != want {
			t.Errorf("parseAwakeFor(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"2 hours", "-5m", "10ms"} {
		if _, err := parseAwakeFor(bad); err == nil {
			t.Errorf("parseAwakeFor(%q) should fail", bad)
		}
	}
}

func TestCaffeinateArgs(t *testing.T) {
	if got := strings.Join(caffeinateArgs(0, false), " "); got != "-i -m" {
		t.Errorf("open-ended: %q", got)
	}
	if got := strings.Join(caffeinateArgs(2*time.Hour, true), " "); got != "-i -m -d -t 7200" {
		t.Errorf("2h display: %q", got)
	}
}

func TestInhibitArgs(t *testing.T) {
	got := strings.Join(inhibitArgs(0, false), " ")
	if !strings.Contains(got, "--what=sleep ") || !strings.HasSuffix(got, "sleep infinity") {
		t.Errorf("open-ended: %q", got)
	}
	got = strings.Join(inhibitArgs(time.Minute, true), " ")
	if !strings.Contains(got, "--what=sleep:idle") || !strings.HasSuffix(got, "sleep 60") {
		t.Errorf("1m display: %q", got)
	}
}

func TestDescribeAwake(t *testing.T) {
	if got := describeAwake(awakeState{}); got != "keep-awake off" {
		t.Errorf("off: %q", got)
	}
	got := describeAwake(awakeState{Active: true, PID: 7, Display: true, Until: "2026-11-07T10:00:00Z"})
	if !strings.Contains(got, "display on") || !strings.Contains(got, "2026-11-07T10:00:00Z") {
		t.Errorf("on: %q", got)
	}
}

// A record whose child has died (the duration ran out) reads as off and is
// cleaned up, so status never reports a hold that no longer exists.
func TestCurrentAwakeDropsDeadChild(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
	t.Setenv("LocalAppData", dir)
	if err := saveAwake(awakeState{Active: true, PID: 999999}); err != nil {
		t.Fatal(err)
	}
	if st := currentAwake(); st.Active {
		t.Errorf("dead child reported active: %+v", st)
	}
	if _, err := os.Stat(awakePath()); !os.IsNotExist(err) {
		t.Errorf("stale record left behind: %v", err)
	}
	if stopAwake() {
		t.Error("stopAwake with nothing live should report false")
	}
}
