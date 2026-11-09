package groups

import (
	"strings"
	"testing"
	"time"
)

func TestParseIdleWatch(t *testing.T) {
	for _, tc := range []struct {
		forValue string
		active   bool
		ok       bool
	}{
		{"10m", false, true},
		{"90s", false, true},
		{"", true, true},
		{"", false, false},
		{"10m", true, false},
		{"soon", false, false},
		{"0s", false, false},
	} {
		_, err := parseIdleWatch(tc.forValue, tc.active)
		if (err == nil) != tc.ok {
			t.Errorf("for=%q active=%v: err=%v", tc.forValue, tc.active, err)
		}
	}
}

func TestIdleWatchFor(t *testing.T) {
	w, _ := parseIdleWatch("10s", false)
	if w.observe(3*time.Second) || w.observe(9999*time.Millisecond) || !w.observe(10*time.Second) {
		t.Fatal("--for threshold")
	}
	// Already idle at the first sample counts.
	w, _ = parseIdleWatch("10s", false)
	if !w.observe(time.Minute) {
		t.Fatal("already idle")
	}
}

func TestIdleWatchActive(t *testing.T) {
	w, _ := parseIdleWatch("", true)
	// The first sample is the baseline, however large or small.
	if w.observe(40*time.Second) || w.observe(41*time.Second) || w.observe(42*time.Second) {
		t.Fatal("grew while idle")
	}
	if !w.observe(300 * time.Millisecond) {
		t.Fatal("input resets the counter")
	}
}

func fakeIdleSamples(t *testing.T, samples []time.Duration) {
	t.Helper()
	old := idleFn
	i := 0
	idleFn = func() (time.Duration, bool) {
		s := samples[min(i, len(samples)-1)]
		i++
		return s, true
	}
	t.Cleanup(func() { idleFn = old })
}

func TestRunWatchIdleLoop(t *testing.T) {
	fakeIdleSamples(t, []time.Duration{time.Second, 5 * time.Second, 12 * time.Second})
	c, out := pageCtx()
	if err := runWatchIdle(c, []string{"--for=10s", "--interval=250ms", "--json"}); err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{`"event":"idle"`, `"state":"idle"`, `"idle_ms":12000`} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %s in %s", want, line)
		}
	}

	fakeIdleSamples(t, []time.Duration{30 * time.Second, 31 * time.Second, 200 * time.Millisecond})
	c, out = pageCtx()
	if err := runWatchIdle(c, []string{"--active", "--interval=250ms", "--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"state":"active"`) {
		t.Fatalf("active: %s", out.String())
	}
}

func TestRunWatchIdleTimeoutAndFlags(t *testing.T) {
	fakeIdleSamples(t, []time.Duration{time.Second})
	c, _ := pageCtx()
	if err := runWatchIdle(c, []string{"--for=1h", "--interval=250ms", "--timeout=300ms"}); exitCode(err) != exitNotFound {
		t.Fatalf("timeout: %v", err)
	}
	for name, args := range map[string][]string{
		"no mode":     {},
		"both":        {"--for=1m", "--active"},
		"fast":        {"--for=1m", "--interval=10ms"},
		"bad timeout": {"--for=1m", "--timeout=never"},
		"unknown":     {"--for=1m", "--nope"},
	} {
		if err := runWatchIdle(c, args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	old := idleFn
	idleFn = func() (time.Duration, bool) { return 0, false }
	defer func() { idleFn = old }()
	if err := runWatchIdle(c, []string{"--for=1m"}); err == nil || !strings.Contains(err.Error(), "cannot report") {
		t.Fatalf("unknown idle: %v", err)
	}
}
