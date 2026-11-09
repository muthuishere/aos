package groups

import (
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/engine"
)

func TestParseTextCond(t *testing.T) {
	for _, tc := range []struct {
		match, gone string
		fresh       bool
		ok          bool
	}{
		{"a", "", false, true},
		{"", "a", false, true},
		{"a", "", true, true},
		{"", "", false, false},
		{"a", "b", false, false},
		{"", "b", true, false},
	} {
		_, err := parseTextCond(tc.match, tc.gone, false, tc.fresh)
		if (err == nil) != tc.ok {
			t.Errorf("match=%q gone=%q fresh=%v: err=%v", tc.match, tc.gone, tc.fresh, err)
		}
	}
}

func TestTextHas(t *testing.T) {
	text := "Inbox\nMuthu: are you there?\nDone"
	for _, tc := range []struct {
		want  string
		exact bool
		has   bool
	}{
		{"muthu", false, true},
		{"are you", false, true},
		{"muthu", true, false},
		{"done", true, true},
		{"  DONE ", true, false},
		{"missing", false, false},
	} {
		if got := textHas(text, tc.want, tc.exact); got != tc.has {
			t.Errorf("textHas(%q, exact=%v) = %v", tc.want, tc.exact, got)
		}
	}
}

func TestTextStateMatchGoneNew(t *testing.T) {
	s := &textState{cond: textCond{match: "ping"}}
	if s.observe("nothing", true) || s.observe("", false) || !s.observe("a Ping arrived", true) {
		t.Fatal("match: appears")
	}
	// Plain --match is a condition: text already there counts.
	if !(&textState{cond: textCond{match: "ping"}}).observe("ping", true) {
		t.Fatal("match: already present")
	}

	g := &textState{cond: textCond{gone: "Syncing"}}
	if g.observe("Syncing...", true) || !g.observe("Synced", true) {
		t.Fatal("gone: disappears")
	}
	if !(&textState{cond: textCond{gone: "x"}}).observe("", false) {
		t.Fatal("gone: closed window counts as gone")
	}

	// --new ignores text present at the first read, until it was seen absent.
	n := &textState{cond: textCond{match: "ping", fresh: true}}
	if n.observe("ping", true) || n.observe("ping", true) {
		t.Fatal("new: baseline hit counted")
	}
	if n.observe("quiet", true) || !n.observe("ping again", true) {
		t.Fatal("new: reappearance")
	}
	n = &textState{cond: textCond{match: "ping", fresh: true}}
	if n.observe("quiet", true) || !n.observe("ping", true) {
		t.Fatal("new: appearance after a clean baseline")
	}
}

func TestRunWatchWindowTextFlags(t *testing.T) {
	c, _ := pageCtx()
	for name, args := range map[string][]string{
		"no condition": {"--app=Teams"},
		"both":         {"--app=Teams", "--match=a", "--gone=b"},
		"no window":    {"--match=a"},
		"max":          {"--app=Teams", "--match=a", "--max=1"},
		"fast":         {"--app=Teams", "--match=a", "--interval=100ms"},
		"bad timeout":  {"--app=Teams", "--match=a", "--timeout=soon"},
		"unknown":      {"--app=Teams", "--match=a", "--bogus"},
	} {
		if err := runWatchWindow(c, args); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func fakeTextWindow(t *testing.T, texts []string) *int {
	t.Helper()
	orig := textWindow
	t.Cleanup(func() { textWindow = orig })
	i := 0
	textWindow = func(engine.Match, engine.FindSource) (engine.WindowText, bool, error) {
		text := texts[min(i, len(texts)-1)]
		i++
		w := engine.Window{ID: "7", App: "Teams", Title: "Chat"}
		return engine.WindowText{Window: w, App: "Teams", Text: text}, true, nil
	}
	return &i
}

func TestWatchWindowTextEndToEnd(t *testing.T) {
	fakeTextWindow(t, []string{"hello", "hello", "hello\nMuthu: lunch?"})
	// resolveMatch needs a real window list; the loop is exercised directly
	// with the same read seam the command uses.
	c, out := pageCtx()
	cond, _ := parseTextCond("muthu", "", false, false)
	err := textWatchLoop(c, cond, time.Millisecond, 0, true, "app Teams", func() (engine.WindowText, bool, error) {
		return textWindow(engine.Match{App: "Teams"}, "")
	})
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{`"event":"window-text"`, `"app":"Teams"`, `"match":"muthu"`, `"line":"Muthu: lunch?"`} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %s in %s", want, line)
		}
	}
}

func TestWatchWindowTextTimeoutExit3(t *testing.T) {
	fakeTextWindow(t, []string{"hello"})
	c, _ := pageCtx()
	cond, _ := parseTextCond("nope", "", false, false)
	err := textWatchLoop(c, cond, time.Millisecond, 20*time.Millisecond, false, "app Teams", func() (engine.WindowText, bool, error) {
		return textWindow(engine.Match{App: "Teams"}, "")
	})
	if exitCode(err) != exitNotFound {
		t.Fatalf("timeout: %v", err)
	}
	cond, _ = parseTextCond("", "hello", false, false)
	err = textWatchLoop(c, cond, time.Millisecond, 20*time.Millisecond, false, "app Teams", func() (engine.WindowText, bool, error) {
		return textWindow(engine.Match{App: "Teams"}, "")
	})
	if exitCode(err) != exitNotFound || !strings.Contains(err.Error(), "did not disappear") {
		t.Fatalf("gone timeout: %v", err)
	}
}
