package groups

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

// What a script may never contain when a page command (or a plain list) can
// run it: anything that switches the active tab, raises a window or activates
// the app.
var focusStealing = []string{
	"activeTabIndex =", "activeTabIndex=", "selectTab(", ".select()", "activate()",
	"activateWindow(", "index = 1", "currentTab =", "selectedTab =",
}

func allFamilies() map[string]*scriptFamily {
	return map[string]*scriptFamily{
		"chrome": chromeFamily(), "safari": safariFamily(), "ghostty": ghosttyFamily(),
		"iterm": itermFamily(), "terminal": terminalFamily(),
	}
}

// listJS runs for every page command (to find the tab), sendJS for
// `tab send`, closeJS for `tab close`: none of them selects or raises.
func TestReadScriptsNeverStealFocus(t *testing.T) {
	for name, f := range allFamilies() {
		for what, js := range map[string]string{"list": f.listJS, "send": f.sendJS, "close": f.closeJS} {
			for _, bad := range focusStealing {
				if strings.Contains(js, bad) {
					t.Errorf("%s %sJS contains %q:\n%s", name, what, bad, js)
				}
			}
		}
	}
}

func TestOpenScriptsActivateOnlyWhenNotBackground(t *testing.T) {
	for name, f := range allFamilies() {
		for _, line := range strings.Split(f.openJS, "\n") {
			if !strings.Contains(line, "activate()") && !strings.Contains(line, "index = 1") {
				continue
			}
			if !strings.Contains(line, "!bg") && !strings.Contains(line, `argv[4] != "1"`) {
				t.Errorf("%s openJS raises or activates without checking the background flag: %q", name, line)
			}
		}
	}
}

// A page command aimed at a tab its window is not showing is refused, and
// no script runs except the listing.
func TestPageCommandRefusesABackgroundTab(t *testing.T) {
	var ran []string
	fakeTabs(t, nil, func(s string, args []string) (string, error) {
		ran = append(ran, s)
		return listScript(s, args)
	})
	_, _, err := resolvePageTab(tabFilter{Kind: "browser", URL: "a.example"}) // tab 11 is not active
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != exitNotFound ||
		!strings.Contains(err.Error(), "aos tab focus --id=chrome:100:11") || !strings.Contains(err.Error(), "--focus") {
		t.Fatalf("want a refusal that names `aos tab focus` and --focus, got %v", err)
	}
	if len(ran) == 0 {
		t.Fatal("expected the list script to run")
	}
	for _, s := range ran {
		for _, bad := range focusStealing {
			if strings.Contains(s, bad) {
				t.Fatalf("a page command ran a script containing %q:\n%s", bad, s)
			}
		}
	}
}

// --focus is the only way a page command may select, and it runs the
// family's select script, never the focus script.
func TestFocusFlagSelectsWithSelectNotFocusScript(t *testing.T) {
	var ran []string
	fakeTabs(t, nil, listScript)
	tab, err := pickTab(tabFilter{Kind: "browser", URL: "a.example"}, true)
	if err != nil {
		t.Fatal(err)
	}
	fakeTabs(t, nil, func(s string, args []string) (string, error) {
		ran = append(ran, s)
		return "ok", nil
	})
	if err := chromeFamily().selectQuiet(context.Background(), tab); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || !strings.Contains(ran[0], "activeTabIndex =") || strings.Contains(ran[0], "activate()") || strings.Contains(ran[0], "index = 1") {
		t.Fatalf("select ran %q", ran)
	}
}

func TestTabOpenIsBackgroundByDefault(t *testing.T) {
	cases := []struct {
		args []string
		bg   string
		err  bool
	}{
		{[]string{"https://x.example", "--app=Google Chrome"}, "1", false},
		{[]string{"https://x.example", "--app=Google Chrome", "--background"}, "1", false},
		{[]string{"https://x.example", "--app=Google Chrome", "--focus"}, "0", false},
		{[]string{"https://x.example", "--app=Google Chrome", "--focus", "--background"}, "", true},
	}
	for _, c := range cases {
		calls := fakeTabs(t, nil, func(s string, args []string) (string, error) {
			return `{"window":"100","tab":"99"}`, nil
		})
		_, err := runTab(t, runTabOpen, c.args...)
		if c.err {
			if err == nil {
				t.Errorf("%v: want an error", c.args)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%v: %v", c.args, err)
		}
		last := (*calls)[len(*calls)-1]
		// app, url, window, newWindow, bg, cwd, command
		if last[4] != c.bg {
			t.Errorf("%v: bg=%s, want %s (args %q)", c.args, last[4], c.bg, last)
		}
	}
}

// propertySetters returns the assignments in a JXA program whose target is a
// property or an element (anything with a "." or "[" in it) rather than a
// plain local variable. In JXA, `w.activeTabIndex = 2` is a write sent to the
// app. `tab list` is polled every few seconds, so its scripts must hold none.
func propertySetters(js string) []string {
	const ident = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_$.[]"
	var out []string
	for i := 0; i < len(js); i++ {
		if js[i] != '=' {
			continue
		}
		if i > 0 && strings.ContainsRune("=!<>", rune(js[i-1])) {
			continue
		}
		if i+1 < len(js) && (js[i+1] == '=' || js[i+1] == '>') {
			continue
		}
		j := i
		for j > 0 && js[j-1] == ' ' {
			j--
		}
		k := j
		for k > 0 && strings.ContainsRune(ident, rune(js[k-1])) {
			k--
		}
		if target := js[k:j]; strings.ContainsAny(target, ".[") {
			out = append(out, target)
		}
	}
	return out
}

var listForbidden = regexp.MustCompile(`activate|\bselect\b|selectTab|index\s*=|launch|reopen|frontmost|\.make\(|\.close\(|\.push\(A\.`)

func TestListScriptsAreReadOnly(t *testing.T) {
	for name, f := range allFamilies() {
		if got := propertySetters(f.listJS); len(got) > 0 {
			t.Errorf("%s listJS sets properties %q", name, got)
		}
		if m := listForbidden.FindString(f.listJS); m != "" {
			t.Errorf("%s listJS contains %q", name, m)
		}
		if !strings.Contains(f.listJS, ".running()") {
			t.Errorf("%s listJS must check running() first, so listing never launches the app", name)
		}
	}
	// The detector itself.
	if len(propertySetters("w.activeTabIndex = 2; var x = 1; y = 2; a == b; r[1] = 3")) != 2 {
		t.Error("propertySetters misses a setter or flags a local")
	}
}
