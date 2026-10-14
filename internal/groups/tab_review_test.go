package groups

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func fakeBrowserPath(exe string) (string, error) { return `C:\Program Files\B\` + exe, nil }

func TestWindowsOpenNeverGoesThroughCmd(t *testing.T) {
	for _, u := range []string{`x" & calc & "`, `https://a.example/?q=1&x=|calc`, `^& calc`, `%COMSPEC%`} {
		name, args, err := windowsOpenCommand(tabOpenRequest{App: "msedge", URL: u}, fakeBrowserPath)
		if err != nil {
			t.Fatalf("%q: %v", u, err)
		}
		if name != `C:\Program Files\B\msedge.exe` || !reflect.DeepEqual(args, []string{"--", u}) {
			t.Fatalf("%q -> %q %q: the URL must be one argv entry after --", u, name, args)
		}
	}
	name, args, _ := windowsOpenCommand(tabOpenRequest{App: "Google Chrome", URL: "https://a", NewWindow: true}, fakeBrowserPath)
	if !strings.HasSuffix(name, "chrome.exe") || !reflect.DeepEqual(args, []string{"--new-window", "--", "https://a"}) {
		t.Fatalf("%q %q", name, args)
	}
}

func TestWindowsOpenRefusesHostileURLs(t *testing.T) {
	for _, u := range []string{"https://a\n& calc", "https://a\r", "a\x00b", "--gpu-launcher=calc", " -foo"} {
		if _, _, err := windowsOpenCommand(tabOpenRequest{App: "msedge", URL: u}, fakeBrowserPath); err == nil {
			t.Errorf("%q should be refused", u)
		}
		if err := checkTabURL(u); err == nil {
			t.Errorf("checkTabURL(%q) should fail", u)
		}
	}
	if _, _, err := windowsOpenCommand(tabOpenRequest{App: "msedge"}, func(string) (string, error) {
		return "", errors.New("not installed")
	}); err == nil {
		t.Fatal("a missing browser must be an error, not a fallback to cmd")
	}
}

func TestWindowsTerminalArgv(t *testing.T) {
	name, args, err := windowsOpenCommand(tabOpenRequest{App: "WindowsTerminal", CWD: `C:\a;split-pane -V`, Command: "dir & echo x; new-tab"}, fakeBrowserPath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-w", "0", "new-tab", "-d", `C:\a\;split-pane -V`, "cmd", "/k", `dir & echo x\; new-tab`}
	if name != "wt.exe" || !reflect.DeepEqual(args, want) {
		t.Fatalf("%q %q", name, args)
	}
	if _, _, err := windowsOpenCommand(tabOpenRequest{App: "wt", CWD: "C:\\a\nb"}, fakeBrowserPath); err == nil {
		t.Fatal("a multi-line --cwd should be refused")
	}
	if _, _, err := windowsOpenCommand(tabOpenRequest{App: "wt", URL: "https://a"}, fakeBrowserPath); err == nil {
		t.Fatal("wt takes no URL")
	}
}

func TestCloseChords(t *testing.T) {
	cases := map[string]string{"WindowsTerminal": "ctrl+shift+w", "msedge": "ctrl+w", "chrome.exe": "ctrl+w", "Notepad": "ctrl+w", "Slack": "", "Code": ""}
	for app, want := range cases {
		if got := windowsCloseChord(app); got != want {
			t.Errorf("windowsCloseChord(%q) = %q, want %q", app, got, want)
		}
	}
	if macCloseChord("Firefox") != "cmd+w" || macCloseChord("Calculator") != "" {
		t.Fatal("mac chords")
	}
}

const threeTabsJSON = `{"running":true,"windows":[{"id":"100","title":"tab one","tabs":[
 {"id":"1","index":1,"title":"tab one","url":"https://one/","active":true},
 {"id":"2","index":2,"title":"tab two","url":"https://two/","active":false},
 {"id":"3","index":3,"title":"other","url":"https://three/","active":false}]}]}`

func threeTabs(s string, args []string) (string, error) {
	if args[0] == "Google Chrome" && strings.Contains(s, "activeTabIndex()") {
		return threeTabsJSON, nil
	}
	return listScript(s, args)
}

func TestCloseRefusesAmbiguousMatch(t *testing.T) {
	calls := fakeTabs(t, nil, threeTabs)
	_, err := runTab(t, runTabClose, "--title=tab", "--app=chrome")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != exitAmbiguous {
		t.Fatalf("want exit 4, got %v", err)
	}
	if !strings.Contains(exit.Message, "chrome:100:1") || !strings.Contains(exit.Message, "chrome:100:2") {
		t.Fatalf("candidates missing: %s", exit.Message)
	}
	for _, c := range *calls {
		if len(c) > 1 {
			t.Fatalf("nothing may be closed, but ran %v", c)
		}
	}
	// --exact narrows it to one, which closes.
	if _, err := runTab(t, runTabClose, "--title=tab two", "--exact", "--app=chrome"); err != nil {
		t.Fatal(err)
	}
	// focus still picks the best match.
	if out, err := runTab(t, runTabFocus, "--title=tab", "--app=chrome"); err != nil || !strings.HasPrefix(out, "chrome:100:1") {
		t.Fatalf("focus: %q %v", out, err)
	}
}

func TestExpectTitleGuardsStaleIDs(t *testing.T) {
	fakeTabs(t, nil, threeTabs)
	_, err := runTab(t, runTabClose, "--id=chrome:100:2", "--expect-title=Inbox")
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != exitNotFound || !strings.Contains(exit.Message, "tab two") {
		t.Fatalf("want exit 3 naming the current title, got %v", err)
	}
	if _, err := runTab(t, runTabClose, "--id=chrome:100:2", "--expect-title=two"); err != nil {
		t.Fatal(err)
	}
}

// fakeAX is an accessibility provider over a scripted tab strip.
func fakeAX(front string, strips ...[]string) (*axTabs, *[]string) {
	var pressed []string
	n := 0
	w := engine.Window{ID: "9", App: "WindowsTerminal", Title: "a", Bounds: engine.Rect{W: 800, H: 600}}
	a := newAXTabs("uia", windowsCloseChord, nil)
	a.windows = func() ([]engine.Window, error) {
		return []engine.Window{w, {ID: front, Focused: true, App: "x"}}, nil
	}
	a.texts = func(string) ([]engine.TextItem, error) {
		strip := strips[n]
		if n < len(strips)-1 {
			n++
		}
		var items []engine.TextItem
		for i, s := range strip {
			items = append(items, engine.TextItem{Text: s, Role: "tab", Bounds: engine.Rect{X: 10 + 100*i, Y: 5, W: 90, H: 20}})
		}
		return items, nil
	}
	a.pressKey = func(c string) error { pressed = append(pressed, c); return nil }
	a.sleep = func(time.Duration) {}
	a.selectTab = func(context.Context, Tab) error { return nil }
	return a, &pressed
}

func TestAXCloseChecksFocusAndVerifies(t *testing.T) {
	tab := Tab{ID: "uia:9:2", App: "WindowsTerminal", Window: "9", Title: "b"}

	a, pressed := fakeAX("9", []string{"a", "b"}, []string{"a"})
	if err := a.close(context.Background(), tab); err != nil || strings.Join(*pressed, ",") != "ctrl+shift+w" {
		t.Fatalf("close: %v, pressed %v", err, *pressed)
	}

	a, pressed = fakeAX("77", []string{"a", "b"})
	if err := a.close(context.Background(), tab); err == nil || len(*pressed) != 0 {
		t.Fatalf("another window in front: must not press, got %v %v", err, *pressed)
	}

	a, _ = fakeAX("9", []string{"a", "b"})
	if err := a.close(context.Background(), tab); err == nil || !strings.Contains(err.Error(), "did not drop") {
		t.Fatalf("unchanged strip must not report success: %v", err)
	}

	a, pressed = fakeAX("9", []string{"a", "b"})
	slack := Tab{ID: "uia:9:2", App: "Slack", Window: "9"}
	if err := a.close(context.Background(), slack); err == nil || len(*pressed) != 0 {
		t.Fatalf("unknown app must be refused, not guessed: %v %v", err, *pressed)
	}
}
