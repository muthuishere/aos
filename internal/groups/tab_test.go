package groups

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func TestWindowKind(t *testing.T) {
	cases := map[string]string{
		"Google Chrome": "browser", "msedge": "browser", "Safari": "browser", "firefox.exe": "browser",
		"Ghostty": "terminal", "iTerm2": "terminal", "Terminal": "terminal", "WindowsTerminal": "terminal",
		"Code": "editor", "Notepad": "editor", "Finder": "files", "explorer": "files",
		"Slack": "other", "Calculator": "other",
	}
	for app, want := range cases {
		if got := windowKind(app); got != want {
			t.Errorf("windowKind(%q) = %q, want %q", app, got, want)
		}
	}
}

func TestCheckKind(t *testing.T) {
	if err := checkKind("browser"); err != nil {
		t.Fatal(err)
	}
	if err := checkKind("browsers"); err == nil {
		t.Fatal("want an error for an unknown kind")
	}
}

func TestSplitTabID(t *testing.T) {
	slug, parts, err := splitTabID("ghostty:tab-group-1:tab-2:ABC")
	if err != nil || slug != "ghostty" || len(parts) != 3 || parts[2] != "ABC" {
		t.Fatalf("got %q %v %v", slug, parts, err)
	}
	for _, bad := range []string{"", "chrome", "chrome:1", "chrome::2", ":1:2"} {
		if _, _, err := splitTabID(bad); err == nil {
			t.Errorf("splitTabID(%q) should fail", bad)
		}
	}
}

const chromeJSON = `{"running":true,"windows":[
 {"id":"100","title":"B tab","bounds":{"x":-1700,"y":60,"width":1200,"height":800},"minimized":false,
  "tabs":[{"id":"11","index":1,"title":"A tab","url":"https://a.example/","active":false},
          {"id":"12","index":2,"title":"B tab","url":"https://b.example/x","active":true}]},
 {"id":"200","title":"C tab","bounds":{"x":0,"y":30,"width":800,"height":600},"minimized":true,
  "tabs":[{"id":"21","index":1,"title":"C tab","url":"https://c.example/","active":true}]}]}`

const ghosttyJSON = `{"running":true,"windows":[{"id":"tab-group-9","title":"zsh","tabs":[
 {"id":"tab-1","index":1,"title":"zsh","cwd":"/src/aos","active":true,
  "panes":[{"id":"P1","title":"zsh","cwd":"/src/aos","active":true},{"id":"P2","title":"logs","cwd":"/var/log","active":false}]},
 {"id":"tab-2","index":2,"title":"vim","cwd":"/etc","active":false,"panes":[{"id":"P3","title":"vim","cwd":"/etc","active":true}]}]}]}`

func TestParseScriptTabsChrome(t *testing.T) {
	tabs, err := parseScriptTabs("chrome", "Google Chrome", chromeJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs) != 3 {
		t.Fatalf("got %d tabs", len(tabs))
	}
	b := tabs[1]
	if b.ID != "chrome:100:12" || b.Index != 2 || !b.Active || b.URL != "https://b.example/x" || b.NativeWindow != "100" {
		t.Fatalf("bad tab %+v", b)
	}
	if b.windowBounds == nil || *b.windowBounds != (engine.Rect{X: -1700, Y: 60, W: 1200, H: 800}) {
		t.Fatalf("bounds %+v", b.windowBounds)
	}
	if !tabs[2].minimized {
		t.Fatal("minimized lost")
	}
}

func TestParseScriptTabsIndexIDsAndPanes(t *testing.T) {
	safari := `{"running":true,"windows":[{"id":"7","title":"x","tabs":[{"index":1,"title":"x","url":"u","active":true}]}]}`
	tabs, err := parseScriptTabs("safari", "Safari", safari)
	if err != nil || tabs[0].ID != "safari:7:1" {
		t.Fatalf("index id: %+v %v", tabs, err)
	}
	tabs, err = parseScriptTabs("ghostty", "Ghostty", ghosttyJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(tabs[0].Panes) != 2 || tabs[0].Panes[1].ID != "ghostty:tab-group-9:tab-1:P2" {
		t.Fatalf("panes %+v", tabs[0].Panes)
	}
	if len(tabs[1].Panes) != 0 {
		t.Fatal("a single-pane tab should not list its pane")
	}
	if _, err := parseScriptTabs("x", "X", "not json"); err == nil {
		t.Fatal("want a parse error")
	}
	if tabs, err := parseScriptTabs("x", "X", `{"running":false}`); err != nil || len(tabs) != 0 {
		t.Fatal("not running should be no tabs")
	}
}

func TestStampWindowsByBoundsThenTitle(t *testing.T) {
	tabs, _ := parseScriptTabs("chrome", "Google Chrome", chromeJSON)
	windows := []engine.Window{
		// Same title as window 100 but other bounds: the bounds match wins.
		{ID: "9", App: "Google Chrome", Title: "B tab", Bounds: engine.Rect{X: 5, Y: 5, W: 10, H: 10}},
		{ID: "8", App: "Google Chrome", Title: "B tab", Bounds: engine.Rect{X: -1700, Y: 60, W: 1200, H: 800}},
		{ID: "7", App: "Slack", Title: "C tab", Bounds: engine.Rect{X: 0, Y: 30, W: 800, H: 600}},
	}
	stampWindows(tabs, windows)
	if tabs[0].Window != "8" || tabs[1].Window != "8" {
		t.Fatalf("window 100 -> %q", tabs[0].Window)
	}
	if tabs[2].Window != "" {
		t.Fatal("a minimized window has no on-screen aos window")
	}

	g, _ := parseScriptTabs("ghostty", "Ghostty", ghosttyJSON)
	stampWindows(g, []engine.Window{{ID: "720", App: "Ghostty", Title: "zsh"}})
	if g[0].Window != "720" {
		t.Fatalf("title match failed: %+v", g[0])
	}
}

func TestTabFilterSelects(t *testing.T) {
	tabs, _ := parseScriptTabs("ghostty", "Ghostty", ghosttyJSON)
	for i := range tabs {
		tabs[i].Kind = "terminal"
	}
	cases := []struct {
		f    tabFilter
		want int
	}{
		{tabFilter{}, 2},
		{tabFilter{App: "ghost"}, 2},
		{tabFilter{App: "chrome"}, 0},
		{tabFilter{Kind: "browser"}, 0},
		{tabFilter{CWD: "/var/log"}, 1}, // a pane's cwd counts
		{tabFilter{Title: "VIM"}, 1},
		{tabFilter{Title: "vi", Exact: true}, 0},
		{tabFilter{ID: "ghostty:tab-group-9:tab-1:P2"}, 1},
		{tabFilter{ID: "ghostty:tab-group-9:tab-2"}, 1},
		{tabFilter{Window: "tab-group-9"}, 2},
	}
	for _, c := range cases {
		n := 0
		for _, tb := range tabs {
			if c.f.selects(tb) {
				n++
			}
		}
		if n != c.want {
			t.Errorf("%+v selected %d, want %d", c.f, n, c.want)
		}
	}
}

func TestSortTabs(t *testing.T) {
	tabs := []Tab{{App: "Safari", NativeWindow: "1", Index: 2}, {App: "Ghostty", NativeWindow: "b", Index: 1},
		{App: "Safari", NativeWindow: "1", Index: 1}, {App: "Ghostty", NativeWindow: "a", Index: 3}}
	sortTabs(tabs)
	got := ""
	for _, t := range tabs {
		got += t.App[:1] + t.NativeWindow + string(rune('0'+t.Index)) + " "
	}
	if got != "Ga3 Gb1 S11 S12 " {
		t.Fatalf("order %q", got)
	}
}

func TestScriptErrPermission(t *testing.T) {
	err := scriptErr("Google Chrome", errors.New("execution error: Not authorized to send Apple events to Google Chrome. (-1743)"))
	if !strings.Contains(err.Error(), "Automation") || !strings.Contains(err.Error(), "tccutil reset AppleEvents") {
		t.Fatalf("unhelpful: %v", err)
	}
	if err := scriptErr("Safari", errors.New("Error: aos-gone: tab 4 (-2700)")); !strings.Contains(err.Error(), "tab 4 is gone") {
		t.Fatalf("gone: %v", err)
	}
}

func TestTabKeyChord(t *testing.T) {
	key, mods, err := tabKeyChord("Ctrl+Shift+C")
	if err != nil || key != "c" || strings.Join(mods, ",") != "control,shift" {
		t.Fatalf("%q %v %v", key, mods, err)
	}
	if key, _, _ := tabKeyChord("return"); key != "enter" {
		t.Fatal(key)
	}
	if _, _, err := tabKeyChord("hyper+x"); err == nil {
		t.Fatal("want an error for an unknown modifier")
	}
}

func TestShellCommands(t *testing.T) {
	t.Setenv("SHELL", "/bin/zsh")
	got := shellCommand("/it's here", "make test")
	want := `/bin/zsh -lc 'cd '\''/it'\''\'\'''\''s here'\'' && make test; exec /bin/zsh -l'`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := plainShellLine("/tmp", "ls"); got != "cd '/tmp' && ls" {
		t.Fatal(got)
	}
}

func TestAXTabsOf(t *testing.T) {
	w := engine.Window{ID: "55", App: "msedge", Title: "Inbox - Mail - Microsoft Edge", Bounds: engine.Rect{X: 0, Y: 0, W: 1000, H: 800}}
	items := []engine.TextItem{
		{Text: "News - Memory usage - 120 MB", Role: "tab", Bounds: engine.Rect{X: 10, Y: 5, W: 200, H: 30}},
		{Text: "Inbox - Mail", Role: "tab", Bounds: engine.Rect{X: 220, Y: 5, W: 200, H: 30}},
		{Text: "Settings", Role: "radio", Bounds: engine.Rect{X: 10, Y: 100, W: 50, H: 20}},
		{Text: "File", Role: "tab", Bounds: engine.Rect{X: 10, Y: -20, W: 40, H: 20}}, // menu bar, outside
	}
	tabs := axTabsOf("uia", w, items)
	if len(tabs) != 2 {
		t.Fatalf("got %+v", tabs)
	}
	if tabs[0].Title != "News" || tabs[0].ID != "uia:55:1" || tabs[0].Active {
		t.Fatalf("first %+v", tabs[0])
	}
	if !tabs[1].Active || tabs[1].Kind != "browser" || tabs[1].center == nil || tabs[1].center.X != 320 {
		t.Fatalf("second %+v", tabs[1])
	}
}

// fakeTabs swaps the window list, the providers and the JXA runner.
func fakeTabs(t *testing.T, windows []engine.Window, script func(script string, args []string) (string, error)) *[][]string {
	t.Helper()
	var calls [][]string
	oldW, oldP, oldR := tabWindows, tabProviders, runJXA
	t.Cleanup(func() { tabWindows, tabProviders, runJXA = oldW, oldP, oldR })
	tabWindows = func() ([]engine.Window, error) { return windows, nil }
	tabProviders = func() []tabProvider { return []tabProvider{chromeFamily(), ghosttyFamily()} }
	runJXA = func(_ context.Context, s string, args ...string) (string, error) {
		calls = append(calls, args)
		return script(s, args)
	}
	return &calls
}

func listScript(s string, args []string) (string, error) {
	switch {
	case args[0] == "Google Chrome" && strings.Contains(s, "activeTabIndex()"):
		return chromeJSON, nil
	case args[0] == "Ghostty" && strings.Contains(s, "focusedTerminal.id()"):
		return ghosttyJSON, nil
	}
	if strings.Contains(s, "running: false") && strings.Contains(s, "function run(argv) {\n  var A; try") {
		return `{"running":false}`, nil
	}
	return "ok", nil
}

func runTab(t *testing.T, run cli.Runner, args ...string) (string, error) {
	t.Helper()
	var out, errb bytes.Buffer
	c := &cli.Ctx{Stdout: &out, Stderr: &errb}
	err := run(c, args)
	return out.String(), err
}

func TestTabFocusNotFoundExits3(t *testing.T) {
	fakeTabs(t, nil, listScript)
	_, err := runTab(t, runTabFocus, "--title=nothing-like-this")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != exitNotFound {
		t.Fatalf("want exit 3, got %v", err)
	}
	if _, err := runTab(t, runTabFocus); err == nil || !strings.Contains(err.Error(), "name a tab") {
		t.Fatalf("want a usage error, got %v", err)
	}
}

func TestTabFocusRunsTheFamilyScript(t *testing.T) {
	calls := fakeTabs(t, nil, listScript)
	out, err := runTab(t, runTabFocus, "--url=a.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "chrome:100:11") {
		t.Fatalf("out %q", out)
	}
	last := (*calls)[len(*calls)-1]
	if strings.Join(last[:3], " ") != "Google Chrome 100 11" {
		t.Fatalf("focus args %v", last)
	}
}

func TestTabSendToPane(t *testing.T) {
	calls := fakeTabs(t, nil, listScript)
	if _, err := runTab(t, runTabSend, "--id=ghostty:tab-group-9:tab-1:P2", "--key=ctrl+c"); err != nil {
		t.Fatal(err)
	}
	last := (*calls)[len(*calls)-1]
	// app, window, tab, pane, text, key, modifiers
	if strings.Join(last, "|") != "Ghostty|tab-group-9|tab-1|P2||c|control" {
		t.Fatalf("send args %q", last)
	}
	if _, err := runTab(t, runTabSend, "--id=chrome:100:11", "--text=hi"); err == nil || !strings.Contains(err.Error(), "no input channel") {
		t.Fatalf("browser send should be refused, got %v", err)
	}
}

func TestTabOpenPrintsNewID(t *testing.T) {
	fakeTabs(t, nil, func(s string, args []string) (string, error) {
		if strings.Contains(s, "A.Tab(props)") {
			return `{"window":"100","tab":"99"}`, nil
		}
		return listScript(s, args)
	})
	out, err := runTab(t, runTabOpen, "https://example.com", "--app=chrome")
	if err != nil || strings.TrimSpace(out) != "chrome:100:99" {
		t.Fatalf("%q %v", out, err)
	}
	if _, err := runTab(t, runTabOpen, "https://example.com"); err == nil {
		t.Fatal("--app is required")
	}
	if _, err := runTab(t, runTabOpen, "--app=Notepadqq"); err == nil || !strings.Contains(err.Error(), "cannot open tabs") {
		t.Fatalf("unknown app: %v", err)
	}
}

func TestTabListJSONAndProviderErrors(t *testing.T) {
	fakeTabs(t, []engine.Window{{ID: "8", App: "Google Chrome", Title: "B tab", Bounds: engine.Rect{X: -1700, Y: 60, W: 1200, H: 800}}},
		func(s string, args []string) (string, error) {
			if args[0] == "Ghostty" {
				return "", errors.New("Not authorized to send Apple events to Ghostty. (-1743)")
			}
			return listScript(s, args)
		})
	var out, errb bytes.Buffer
	c := &cli.Ctx{Stdout: &out, Stderr: &errb}
	if err := runTabList(c, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"window": "8"`) || !strings.Contains(out.String(), `"id": "chrome:100:12"`) {
		t.Fatalf("json %s", out.String())
	}
	if !strings.Contains(errb.String(), "Automation permission") {
		t.Fatalf("stderr %q", errb.String())
	}
}
