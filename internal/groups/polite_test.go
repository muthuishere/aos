package groups

import (
	"errors"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func politeCtx(env map[string]string) *cli.Ctx {
	return &cli.Ctx{Env: func(k string) string { return env[k] }}
}

func withIdle(t *testing.T, idle time.Duration, known bool) {
	old := idleFn
	idleFn = func() (time.Duration, bool) { return idle, known }
	t.Cleanup(func() { idleFn = old })
	t.Setenv("HOME", t.TempDir()) // no own-input stamp from a real run
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

func TestPoliteGivesUpWhenUserStaysActive(t *testing.T) {
	withIdle(t, 800*time.Millisecond, true)
	err := politeCheck(politeCtx(map[string]string{"AOS_POLITE_WAIT": "0"}), nil, "window focus")
	var ee *cli.ExitError
	if !errors.As(err, &ee) || ee.Code != exitUserActive {
		t.Fatalf("want exit 5, got %v", err)
	}
}

func TestPoliteAllowsWhenIdleOrOverridden(t *testing.T) {
	withIdle(t, 10*time.Second, true)
	if err := politeCheck(politeCtx(nil), nil, "x"); err != nil {
		t.Fatalf("idle user: %v", err)
	}
	withIdle(t, time.Second, true)
	for _, env := range []map[string]string{{"AOS_POLITE": "0"}, {"AOS_FOCUS": "allow"}} {
		if err := politeCheck(politeCtx(env), nil, "x"); err != nil {
			t.Fatalf("%v: %v", env, err)
		}
	}
	set, _ := parseArgs([]string{"--force"})
	if err := politeCheck(politeCtx(nil), set, "x"); err != nil {
		t.Fatalf("--force: %v", err)
	}
	withIdle(t, 0, false) // platform cannot tell: let it through
	if err := politeCheck(politeCtx(nil), nil, "x"); err != nil {
		t.Fatalf("unknown idle: %v", err)
	}
}

func TestPoliteIgnoresItsOwnInput(t *testing.T) {
	withIdle(t, time.Second, true)
	noteOwnInput() // aos typed just now; the OS idle clock reset because of it
	if err := politeCheck(politeCtx(nil), nil, "key type"); err != nil {
		t.Fatalf("own input counted as the person: %v", err)
	}
}

func TestPoliteRecordsCallerOnSpan(t *testing.T) {
	withIdle(t, time.Second, true)
	c := politeCtx(map[string]string{"AOS_CALLER": "test-agent"})
	c.Env = func(k string) string { return map[string]string{"AOS_CALLER": "test-agent", "AOS_POLITE_WAIT": "0"}[k] }
	_ = politeCheck(c, nil, "window focus")
	got := map[string]bool{}
	for _, a := range c.Extra() {
		got[a.Key] = true
	}
	for _, k := range []string{"agentic_os.focus.action", "agentic_os.focus.caller", "agentic_os.focus.refused", "agentic_os.focus.user_idle_ms"} {
		if !got[k] {
			t.Fatalf("missing %s in %v", k, c.Extra())
		}
	}
}

func TestPoliteWaitsUntilUserStops(t *testing.T) {
	withIdle(t, 0, true)
	calls := 0
	idleFn = func() (time.Duration, bool) {
		calls++
		if calls < 3 {
			return time.Second, true // typing
		}
		return 6 * time.Second, true // stopped
	}
	oldSleep := politeSleep
	politeSleep = func(time.Duration) {}
	defer func() { politeSleep = oldSleep }()
	if err := politeCheck(politeCtx(nil), nil, "key type"); err != nil || calls < 3 {
		t.Fatalf("should wait, then go: err %v calls %d", err, calls)
	}
}
