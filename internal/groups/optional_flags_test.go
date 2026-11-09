package groups

import "testing"

// An optional-value flag is one that is meaningful bare AND with a value.
// `launch Chrome --wait` broke with "--wait needs a value" when `wait` was
// declared as a value flag: the parser demanded a following word. The fix is
// that such a flag is simply left out of valueFlags, so bare `--wait` records
// an empty value that Int/String read as "use the default", `--wait=5000`
// still parses through the `=` form, and the flag never eats a positional.
func TestOptionalValueFlagBareValuedAndDoesNotEatAPositional(t *testing.T) {
	const defaultWait = 10000

	cases := []struct {
		name     string
		args     []string
		wantWait int
		wantRest []string
	}{
		{
			name:     "bare --wait means the default timeout",
			args:     []string{"Chrome", "--wait"},
			wantWait: defaultWait,
			wantRest: []string{"Chrome"},
		},
		{
			name:     "--wait=5000 parses its value",
			args:     []string{"Chrome", "--wait=5000"},
			wantWait: 5000,
			wantRest: []string{"Chrome"},
		},
		{
			name:     "--wait before the app must not swallow it",
			args:     []string{"--wait", "Chrome"},
			wantWait: defaultWait,
			wantRest: []string{"Chrome"},
		},
		{
			name:     "--wait must not swallow any of several positionals",
			args:     []string{"--wait", "Chrome", "notes.txt"},
			wantWait: defaultWait,
			wantRest: []string{"Chrome", "notes.txt"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Exactly how runLaunch parses: `wait` is deliberately not declared
			// as a value flag.
			set, err := parseArgs(tc.args)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if !set.Has("wait") {
				t.Fatal("--wait was not recorded at all")
			}
			wait, err := set.Int("wait", defaultWait)
			if err != nil {
				t.Fatalf("--wait: %v", err)
			}
			if wait != tc.wantWait {
				t.Errorf("wait = %d, want %d", wait, tc.wantWait)
			}
			if len(set.Rest) != len(tc.wantRest) {
				t.Fatalf("rest = %v, want %v", set.Rest, tc.wantRest)
			}
			for i, want := range tc.wantRest {
				if set.Rest[i] != want {
					t.Errorf("rest[%d] = %q, want %q", i, set.Rest[i], want)
				}
			}
			// Reject is what surfaces a typo; the flag itself must be accepted.
			if err := set.Reject("wait"); err != nil {
				t.Errorf("Reject rejected the declared flag: %v", err)
			}
		})
	}
}

// The other half of the contract: an absent optional flag is distinguishable
// from a bare one, because `launch Chrome` must return without waiting.
func TestAbsentOptionalFlagIsNotTheSameAsABareOne(t *testing.T) {
	set, err := parseArgs([]string{"Chrome"})
	if err != nil {
		t.Fatal(err)
	}
	if set.Has("wait") {
		t.Fatal("--wait was not given; Has must say so")
	}
	if ptr, err := set.IntPtr("wait"); err != nil || ptr != nil {
		t.Fatalf("IntPtr = %v, %v; an absent flag is nil", ptr, err)
	}
}

// A bad explicit value must be a clear error, not a silent fall back to the
// default — an agent that types `--wait=soon` needs to be told.
func TestOptionalFlagWithANonNumericValueIsAnError(t *testing.T) {
	set, err := parseArgs([]string{"Chrome", "--wait=soon"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.Int("wait", 10000); err == nil {
		t.Fatal("want an error for --wait=soon")
	}
}

// The contrast case, kept next to the regression so the distinction stays
// visible: a genuine value flag DOES take the following word.
func TestDeclaredValueFlagStillTakesTheNextWord(t *testing.T) {
	set, err := parseArgs([]string{"--timeout", "5000", "Chrome"}, "timeout")
	if err != nil {
		t.Fatal(err)
	}
	if got := set.String("timeout", ""); got != "5000" {
		t.Fatalf("timeout = %q", got)
	}
	if len(set.Rest) != 1 || set.Rest[0] != "Chrome" {
		t.Fatalf("rest = %v", set.Rest)
	}
}

// TestTakeWaitFlagPassesApplicationFlagsThrough pins a usability bug: runLaunch
// parsed the whole command line and rejected anything it did not recognise, so
// `launch Chrome --new-window` failed with "unknown flag --new-window"
// instead of handing that flag to Chrome. Only --wait is ours.
func TestTakeWaitFlagPassesApplicationFlagsThrough(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantRest    []string
		wantWait    bool
		wantTimeout int
	}{
		{"no wait", []string{"Chrome", "--new-window"}, []string{"Chrome", "--new-window"}, false, 10000},
		{"bare wait", []string{"Chrome", "--wait"}, []string{"Chrome"}, true, 10000},
		{"wait with value", []string{"Chrome", "--wait=5000"}, []string{"Chrome"}, true, 5000},
		{"wait among app flags", []string{"Code", "--wait", "--new-window", "-n"}, []string{"Code", "--new-window", "-n"}, true, 10000},
		{"wait before the app", []string{"--wait=250", "Code", "--diff"}, []string{"Code", "--diff"}, true, 250},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rest, timeout, wait, err := takeWaitFlag(tc.args)
			if err != nil {
				t.Fatalf("takeWaitFlag: %v", err)
			}
			if wait != tc.wantWait || timeout != tc.wantTimeout {
				t.Errorf("wait=%v timeout=%d, want %v/%d", wait, timeout, tc.wantWait, tc.wantTimeout)
			}
			if len(rest) != len(tc.wantRest) {
				t.Fatalf("rest = %v, want %v", rest, tc.wantRest)
			}
			for i := range rest {
				if rest[i] != tc.wantRest[i] {
					t.Fatalf("rest = %v, want %v", rest, tc.wantRest)
				}
			}
		})
	}

	if _, _, _, err := takeWaitFlag([]string{"App", "--wait=soon"}); err == nil {
		t.Error("a non-numeric --wait must be rejected")
	}
}

func TestTakeOrFocusFlag(t *testing.T) {
	rest, on := takeOrFocusFlag([]string{"Chrome", "--or-focus", "--new-window"})
	if !on || len(rest) != 2 || rest[0] != "Chrome" || rest[1] != "--new-window" {
		t.Fatalf("got %v %v", rest, on)
	}
	if _, on := takeOrFocusFlag([]string{"Chrome"}); on {
		t.Fatal("--or-focus not given")
	}
}
