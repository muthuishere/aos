package groups

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func TestSelectQuitTargets(t *testing.T) {
	running := []quitTarget{
		{Name: "Finder", PID: 1},
		{Name: "Slack", PID: 10},
		{Name: "Visual Studio Code", PID: 11},
		{Name: "Ghostty", PID: 12}, // the terminal aos runs in
		{Name: "Safari", PID: 13},
		{Name: "Safari", PID: 14}, // a second process of the same app
		{Name: "explorer.exe", PID: 15},
		{Name: "", PID: 16},
	}
	got := selectQuitTargets(running, splitExcept(" slack , code"), map[int]bool{12: true})
	var names []string
	for _, g := range got {
		names = append(names, g.Name)
	}
	if strings.Join(names, ",") != "Safari" {
		t.Errorf("targets = %v, want only Safari", names)
	}
}

func TestSplitExcept(t *testing.T) {
	if got := splitExcept("Slack, ,VS Code,"); len(got) != 2 || got[0] != "slack" || got[1] != "vs code" {
		t.Errorf("got %q", got)
	}
}

func TestAncestorChain(t *testing.T) {
	parent := parsePidPpid("  1 0\n 50 1\n 60 50\n 70 60\n 99 1\n")
	got := ancestorChain(70, parent)
	for _, pid := range []int{70, 60, 50} {
		if !got[pid] {
			t.Errorf("missing %d in %v", pid, got)
		}
	}
	if got[1] || got[99] {
		t.Errorf("init or a sibling counted as ancestor: %v", got)
	}
	// A cycle in a corrupt table must not loop forever.
	if c := ancestorChain(5, map[int]int{5: 6, 6: 5}); len(c) != 2 {
		t.Errorf("cycle: %v", c)
	}
}

// Without --yes and without a terminal, quit-all lists and refuses: an agent
// cannot quit the person's apps by accident.
func TestConfirmQuitGate(t *testing.T) {
	var out bytes.Buffer
	ok, err := confirmQuit(&cli.Ctx{Stdout: &out}, []string{"Safari", "Mail"}, false)
	var exit *cli.ExitError
	if ok || !errors.As(err, &exit) || exit.Code != 1 || !strings.Contains(exit.Message, "--yes") {
		t.Errorf("non-interactive: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(out.String(), "Safari, Mail") {
		t.Errorf("list not shown: %q", out.String())
	}
	for in, want := range map[string]bool{"y\n": true, "YES\n": true, "\n": false, "n\n": false} {
		ok, err := confirmQuit(&cli.Ctx{Stdout: &out, Stdin: strings.NewReader(in)}, []string{"Safari"}, true)
		if err != nil || ok != want {
			t.Errorf("answer %q: ok=%v err=%v", in, ok, err)
		}
	}
}

func TestWindowOwnersAndOthers(t *testing.T) {
	wins := []engine.Window{
		{ID: "1", App: "Code", PID: 10, Focused: true},
		{ID: "2", App: "Code", PID: 10},
		{ID: "3", App: "Slack", PID: 20},
		{ID: "4", App: "ghost", PID: 0},
	}
	if o := windowOwners(wins); len(o) != 2 || o[0].Name != "Code" || o[1].PID != 20 {
		t.Errorf("owners = %+v", o)
	}
	others := othersThanFront(wins)
	if len(others) != 2 || others[0].ID != "3" {
		t.Errorf("others = %+v", others)
	}
}

func TestDesktopToggle(t *testing.T) {
	if got := desktopToggle("Window manager's \"showing the desktop\" mode: N/A\nshowing the desktop: ON"); got != "off" {
		t.Errorf("on -> %q", got)
	}
	if got := desktopToggle("showing the desktop: OFF"); got != "on" {
		t.Errorf("off -> %q", got)
	}
}

func TestQuitAllRejectsPositional(t *testing.T) {
	if err := runAppQuitAll(&cli.Ctx{}, []string{"Safari"}); err == nil {
		t.Error("a bare app name must not be accepted (it reads like `quit Safari`)")
	}
}
