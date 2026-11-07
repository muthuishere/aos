package groups

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

func TestSwitchTarget(t *testing.T) {
	cases := []struct {
		arg     string
		current bool
		want    bool
	}{{"on", false, true}, {"OFF", true, false}, {"toggle", true, false}, {"toggle", false, true}, {"1", false, true}}
	for _, tc := range cases {
		got, err := switchTarget(tc.arg, tc.current)
		if err != nil || got != tc.want {
			t.Errorf("switchTarget(%q, %v) = %v, %v", tc.arg, tc.current, got, err)
		}
	}
	if _, err := switchTarget("dim", false); err == nil {
		t.Error("unknown word should fail")
	}
}

// runSwitch must only call set when the state actually changes, and print the
// resulting state.
func TestRunSwitch(t *testing.T) {
	state, sets := true, 0
	get := func() (bool, error) { return state, nil }
	set := func(v bool) error { sets++; state = v; return nil }
	run := func(args ...string) string {
		var out bytes.Buffer
		if err := runSwitch(&cli.Ctx{Stdout: &out}, "x", args, get, set); err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out.String())
	}
	if run() != "on" || sets != 0 {
		t.Error("bare should print without setting")
	}
	if run("on") != "on" || sets != 0 {
		t.Error("on when already on should not set")
	}
	if run("toggle") != "off" || sets != 1 || state {
		t.Error("toggle should turn it off")
	}
	failing := func() (bool, error) { return false, errors.New("boom") }
	if err := runSwitch(&cli.Ctx{Stdout: &bytes.Buffer{}}, "x", nil, failing, set); err == nil {
		t.Error("get error should surface")
	}
}

func TestGnomeColorScheme(t *testing.T) {
	if !gnomeColorScheme("'prefer-dark'\n") || gnomeColorScheme("'default'") || gnomeColorScheme("'prefer-light'") {
		t.Error("color-scheme misread")
	}
}
