package groups

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// fakePage is shaped like Chrome's tree for a chat page: a heading whose
// text is also a child, a labelled textarea, a button whose name lives in a
// child, a link, and a list.
func fakePage(extra ...string) []engine.WebNode {
	n := []engine.WebNode{
		{Depth: 0, Role: "AXHeading", Title: "Chat", Level: 1},
		{Depth: 1, Role: "AXStaticText", Value: "Chat"},
		{Depth: 0, Role: "AXGroup"},
		{Depth: 1, Role: "AXStaticText", Value: "Welcome back."},
		{Depth: 0, Role: "AXGroup"},
		{Depth: 1, Role: "AXStaticText", Value: "Message"},
		{Depth: 0, Role: "AXTextArea", Title: "Message", Placeholder: "Type a message"},
		{Depth: 0, Role: "AXButton"},
		{Depth: 1, Role: "AXStaticText", Value: "Send"},
		{Depth: 0, Role: "AXLink", Title: "Help"},
		{Depth: 1, Role: "AXStaticText", Value: "Help"},
		{Depth: 0, Role: "AXList"},
		{Depth: 1, Role: "AXGroup"},
		{Depth: 2, Role: "AXStaticText", Value: "hello"},
	}
	for _, e := range extra {
		n = append(n, engine.WebNode{Depth: 1, Role: "AXGroup"}, engine.WebNode{Depth: 2, Role: "AXStaticText", Value: e})
	}
	return n
}

func TestRenderPage(t *testing.T) {
	got := strings.Join(renderPage(fakePage("world"), true, false), "\n")
	want := strings.Join([]string{
		"# Chat",
		"Welcome back.",
		"Message",
		"[e6] Message (textarea, empty) [e7] Send (button) [e9] Help (link)",
		"- hello",
		"- world",
	}, "\n")
	if got != want {
		t.Fatalf("markdown:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderPageValuesAndTextOnly(t *testing.T) {
	nodes := fakePage()
	nodes[6].Value = "draft"
	lines := renderPage(nodes, false, false)
	if !strings.Contains(strings.Join(lines, "\n"), `Message (textarea: "draft") Send (button)`) {
		t.Fatalf("value not shown: %q", lines)
	}
	// watch text leaves field contents out: typing is not page news.
	for _, l := range watchLines(nodes) {
		if strings.Contains(l, "draft") || strings.Contains(l, "(") {
			t.Fatalf("watch line has field content or annotation: %q", l)
		}
	}
	if got := watchLines(nodes); got[0] != "Chat" || got[len(got)-1] != "hello" {
		t.Fatalf("watch lines keep markers: %q", got)
	}
}

func TestElementRefsAreDeterministic(t *testing.T) {
	a, b := pageElements(fakePage()), pageElements(fakePage())
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same tree, different refs")
	}
	refs := []string{}
	for _, e := range a {
		refs = append(refs, e.Ref+"="+e.Role)
	}
	if want := []string{"e6=textarea", "e7=button", "e9=link"}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("refs %v, want %v", refs, want)
	}
	if a[1].fp != "" || a[1].axRole != "AXButton" || a[0].fp != "Message" {
		t.Fatalf("fingerprints: %+v", a)
	}
	for _, bad := range []string{"7", "ex", "e-1", ""} {
		if _, err := parseElementRef(bad); err == nil {
			t.Errorf("parseElementRef(%q) accepted", bad)
		}
	}
	if n, err := parseElementRef("e12"); err != nil || n != 12 {
		t.Fatalf("parseElementRef(e12) = %d, %v", n, err)
	}
}

func TestMatchElements(t *testing.T) {
	elems := pageElements(fakePage())
	if got := matchElements(elems, "type a", false, nil); len(got) != 1 || got[0].Ref != "e6" {
		t.Fatalf("placeholder match: %+v", got)
	}
	if got := matchElements(elems, "sen", true, nil); len(got) != 0 {
		t.Fatalf("--exact matched a substring: %+v", got)
	}
	if got := matchElements(elems, "e", false, nil); len(got) != 3 {
		t.Fatalf("substring: %d hits", len(got))
	}
	// An exact label beats substring hits.
	more := append(fakePage(), engine.WebNode{Role: "AXButton", Title: "Send later"})
	if got := matchElements(pageElements(more), "send", false, nil); len(got) != 1 || got[0].Label != "Send" {
		t.Fatalf("exact should win: %+v", got)
	}
}

func pageCtx() (*cli.Ctx, *bytes.Buffer) {
	var out bytes.Buffer
	return &cli.Ctx{Stdout: &out, Stderr: io.Discard, Env: os.Getenv}, &out
}

func exitCode(err error) int {
	var e *cli.ExitError
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}

// withFakePage swaps the tab, tree and action seams for one test.
func withFakePage(t *testing.T, nodes []engine.WebNode, act func(index, action int, role, label string) error) *[]string {
	t.Helper()
	var typed []string
	oldTab, oldTree, oldAct, oldType, oldPress := pageTab, webTree, webAct, typeInto, pressInto
	oldPrep, oldFocus, oldFront, oldNow := webPrepare, webFocus, frontWindow, tabNow
	t.Cleanup(func() {
		pageTab, webTree, webAct, typeInto, pressInto = oldTab, oldTree, oldAct, oldType, oldPress
		webPrepare, webFocus, frontWindow, tabNow = oldPrep, oldFocus, oldFront, oldNow
	})
	tab := Tab{ID: "chrome:1:2", Kind: "browser", Active: true, Title: "Chat", URL: "https://chat.example/"}
	pageTab = func(id string) (Tab, engine.Window, error) {
		tab.ID = id
		return tab, engine.Window{ID: "1", App: "Google Chrome", PID: 9}, nil
	}
	webPrepare = func(engine.Window) (func(), error) { return func() {}, nil }
	webFocus = func(_ engine.Window, _, _, index int, role, label string) (engine.WebTarget, error) {
		if err := act(index, engine.WebFocus, role, label); err != nil {
			return nil, err
		}
		return &fakeTarget{}, nil
	}
	frontWindow = func() (engine.Window, error) { return engine.Window{ID: "77", App: "Ghostty"}, nil }
	tabNow = func(string) (Tab, error) { return tab, nil }
	webTree = func(*engine.Window, int, int) ([]engine.WebNode, error) { return nodes, nil }
	webAct = func(_ engine.Window, _, _, index, action int, role, label string) error {
		return act(index, action, role, label)
	}
	typeInto = func(_ engine.Window, s string) error { typed = append(typed, s); return nil }
	pressInto = func(_ engine.Window, k string) error { typed = append(typed, "<"+k+">"); return nil }
	return &typed
}

func TestTabClickByRefVerifiesFingerprint(t *testing.T) {
	var got []any
	withFakePage(t, fakePage(), func(index, action int, role, label string) error {
		got = []any{index, action, role, label}
		return nil
	})
	c, out := pageCtx()
	if err := runTabClick(c, []string{"--id=chrome:1:2", "--element=e7"}); err != nil {
		t.Fatal(err)
	}
	if want := []any{7, engine.WebPress, "AXButton", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("act %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "clicked e7  Send (button)") {
		t.Fatalf("output %q", out.String())
	}
}

func TestTabClickStaleRefRefuses(t *testing.T) {
	withFakePage(t, fakePage(), func(int, int, string, string) error { return engine.ErrWebStale })
	c, _ := pageCtx()
	err := runTabClick(c, []string{"--id=chrome:1:2", "--element=e7"})
	if exitCode(err) != exitNotFound || !strings.Contains(err.Error(), "page changed") {
		t.Fatalf("stale ref: %v", err)
	}
	// A ref that is not an element of the page now is refused before acting.
	acted := false
	withFakePage(t, fakePage(), func(int, int, string, string) error { acted = true; return nil })
	err = runTabClick(c, []string{"--id=chrome:1:2", "--element=e3"})
	if exitCode(err) != exitNotFound || acted {
		t.Fatalf("non-element ref: %v (acted %v)", err, acted)
	}
}

func TestTabClickAmbiguousAndNth(t *testing.T) {
	nodes := append(fakePage(), engine.WebNode{Role: "AXButton", Title: "Send"})
	var idx int
	withFakePage(t, nodes, func(index, _ int, _, _ string) error { idx = index; return nil })
	c, _ := pageCtx()
	err := runTabClick(c, []string{"--id=chrome:1:2", "--find=send"})
	if exitCode(err) != exitAmbiguous || !strings.Contains(err.Error(), "[e14] Send (button)") {
		t.Fatalf("ambiguous: %v", err)
	}
	if err := runTabClick(c, []string{"--id=chrome:1:2", "--find=send", "--nth=2"}); err != nil || idx != 14 {
		t.Fatalf("--nth=2: %v idx %d", err, idx)
	}
	if err := runTabClick(c, []string{"--id=chrome:1:2", "--find=nothing"}); exitCode(err) != exitNotFound {
		t.Fatalf("no match: %v", err)
	}
	if err := runTabClick(c, []string{"--id=chrome:1:2", "--find=send", "--element=e7"}); err == nil {
		t.Fatal("both --find and --element accepted")
	}
	if err := runTabClick(c, []string{"--id=chrome:1:2", "--find=help", "--role=button"}); exitCode(err) != exitNotFound {
		t.Fatalf("--role should filter out the link: %v", err)
	}
}

func TestTabTypeFocusesThenPostsKeys(t *testing.T) {
	var action int
	typed := withFakePage(t, fakePage(), func(index, a int, role, label string) error {
		if index != 6 || role != "AXTextArea" || label != "Message" {
			t.Errorf("act on %d %s %q", index, role, label)
		}
		action = a
		return nil
	})
	c, _ := pageCtx()
	if err := runTabType(c, []string{"--id=chrome:1:2", "--into=message", "--text=hi there", "--enter"}); err != nil {
		t.Fatal(err)
	}
	if action != engine.WebFocus || !reflect.DeepEqual(*typed, []string{"hi there", "<enter>"}) {
		t.Fatalf("action %d typed %q", action, *typed)
	}
	// Only fields are candidates: the Send button is not typed into.
	if err := runTabType(c, []string{"--id=chrome:1:2", "--element=e7", "--text=x"}); err == nil {
		t.Fatal("typed into a button")
	}
}

func TestTabTypeStaleNeverTypes(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return engine.ErrWebStale })
	c, _ := pageCtx()
	err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=secret"})
	if exitCode(err) != exitNotFound || len(*typed) != 0 {
		t.Fatalf("stale: %v typed %q", err, *typed)
	}
}

func TestAddedText(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur []string
		want      []string
	}{
		{"appended", []string{"a", "b"}, []string{"a", "b", "c"}, []string{"c"}},
		{"reordered adds nothing", []string{"a", "b", "c"}, []string{"c", "a", "b"}, nil},
		{"duplicate is news", []string{"ok"}, []string{"ok", "ok"}, []string{"ok"}},
		{"live timestamp is not news", []string{"updated 10:31:02", "x"}, []string{"updated 10:31:05", "x"}, nil},
		{"relative time is not news", []string{"sent 3 minutes ago"}, []string{"sent 4 minutes ago"}, nil},
		{"removed is not news", []string{"a", "b"}, []string{"a"}, nil},
		{"new numbered line is news", []string{"tick 1", "tick 2"}, []string{"tick 1", "tick 2", "tick 3"}, []string{"tick 3"}},
		{"case and spacing folded", []string{"Hello  World"}, []string{"hello world"}, nil},
	}
	for _, tc := range cases {
		if got := addedText(tc.prev, tc.cur); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

func TestWatchTabLoopMatchAndTimeout(t *testing.T) {
	reads := [][]string{{"a"}, {"a"}, {"a", "build started"}, {"a", "build started", "Build finished"}}
	i := 0
	read := func() ([]string, error) {
		r := reads[min(i, len(reads)-1)]
		i++
		return r, nil
	}
	c, out := pageCtx()
	err := watchTabLoop(c, tabWatch{tab: "chrome:1:2", match: "finished", interval: time.Millisecond, read: read,
		now: func() time.Time { return time.Unix(0, 0).UTC() }})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `"added":["Build finished"]`) || !strings.Contains(lines[0], `"event":"tab"`) {
		t.Fatalf("events:\n%s", out.String())
	}

	// Text already on the page when the watch starts is the baseline.
	c, _ = pageCtx()
	err = watchTabLoop(c, tabWatch{tab: "chrome:1:2", match: "a", interval: time.Millisecond, timeout: 20 * time.Millisecond,
		read: func() ([]string, error) { return []string{"a"}, nil }})
	if exitCode(err) != exitNotFound {
		t.Fatalf("timeout: %v", err)
	}

	// A tab that is no longer selected is not read.
	c, _ = pageCtx()
	n := 0
	err = watchTabLoop(c, tabWatch{tab: "chrome:1:2", interval: time.Millisecond, timeout: 20 * time.Millisecond,
		read:     func() ([]string, error) { n++; return []string{strings.Repeat("x", n)}, nil },
		selected: func() (bool, error) { return false, nil }})
	if err != nil || n != 1 {
		t.Fatalf("unselected tab read %d times: %v", n, err)
	}
}

func TestTabTypeRefusesWhenFocusDoesNotLand(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return engine.ErrWebFocus })
	c, _ := pageCtx()
	err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=hello"})
	if exitCode(err) != exitNotFound || len(*typed) != 0 || !strings.Contains(err.Error(), "focus did not land") {
		t.Fatalf("focus not landed: %v typed %q", err, *typed)
	}
}

func TestTabTypeStopsWhenFocusMoves(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	// Focus moves after the first burst (to a box with the same label, say).
	webFocus = func(engine.Window, int, int, int, string, string) (engine.WebTarget, error) {
		return &fakeTarget{focusedFor: 1}, nil
	}
	c, _ := pageCtx()
	text := strings.Repeat("a", typeBurst) + "bbbb"
	err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=" + text, "--enter"})
	if exitCode(err) != exitNotFound || !strings.Contains(err.Error(), fmt.Sprintf("after %d of %d characters", typeBurst, len(text))) {
		t.Fatalf("focus moved: %v", err)
	}
	if !reflect.DeepEqual(*typed, []string{strings.Repeat("a", typeBurst)}) {
		t.Fatalf("typed %q, want only the first burst and no Enter", *typed)
	}
}

func TestTabTypeChecksFocusBeforeEnter(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	webFocus = func(engine.Window, int, int, int, string, string) (engine.WebTarget, error) {
		return &fakeTarget{focusedFor: 1}, nil
	}
	c, _ := pageCtx()
	err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=hi", "--enter"})
	if exitCode(err) != exitNotFound || !reflect.DeepEqual(*typed, []string{"hi"}) {
		t.Fatalf("enter after focus loss: %v typed %q", err, *typed)
	}
}

func TestTabTypeNewlines(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	c, _ := pageCtx()
	if err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=line1\nline2"}); err == nil || len(*typed) != 0 {
		t.Fatalf("newline accepted: %v typed %q", err, *typed)
	}
	if err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=line1\nline2", "--allow-newline"}); err != nil {
		t.Fatal(err)
	}
}

func TestTabTypeRefusesWhenTabChanged(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	for name, now := range map[string]Tab{
		"navigated":    {ID: "chrome:1:2", Active: true, Title: "Chat", URL: "https://evil.example/"},
		"deselected":   {ID: "chrome:1:2", Active: false, Title: "Chat", URL: "https://chat.example/"},
		"title change": {ID: "chrome:1:2", Active: true, Title: "Other", URL: "https://chat.example/"},
	} {
		tabNow = func(string) (Tab, error) { return now, nil }
		c, _ := pageCtx()
		err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=x"})
		if exitCode(err) != exitNotFound || len(*typed) != 0 {
			t.Fatalf("%s: %v typed %q", name, err, *typed)
		}
	}
}

func TestTabTypeFocusGuard(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	// The person is in another window of the same browser.
	frontWindow = func() (engine.Window, error) { return engine.Window{ID: "2", App: "Google Chrome", Title: "Mail"}, nil }
	c, _ := pageCtx()
	err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=x"})
	if err == nil || !strings.Contains(err.Error(), "--allow-focus") || len(*typed) != 0 {
		t.Fatalf("focus guard: %v typed %q", err, *typed)
	}
	if err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=x", "--allow-focus"}); err != nil {
		t.Fatal(err)
	}
	// The tab's own window in front is fine: keys go where the person looks.
	frontWindow = func() (engine.Window, error) { return engine.Window{ID: "1", App: "Google Chrome"}, nil }
	if err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=x"}); err != nil {
		t.Fatal(err)
	}
}

func TestPageCommandsRestorePrepare(t *testing.T) {
	withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	restored := 0
	webPrepare = func(engine.Window) (func(), error) { return func() { restored++ }, nil }
	c, _ := pageCtx()
	_ = runTabRead(c, []string{"--id=chrome:1:2"})
	_ = runTabClick(c, []string{"--id=chrome:1:2", "--element=e7"})
	_ = runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=x"})
	if restored != 3 {
		t.Fatalf("restore ran %d times, want 3", restored)
	}
}

func TestWatchTabFlagsAndClosedTab(t *testing.T) {
	withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	c, _ := pageCtx()
	if err := runWatchTab(c, []string{"--id=chrome:1:2", "--interval=500ms"}); err == nil {
		t.Fatal("sub-second interval accepted")
	}
	err := watchTabLoop(c, tabWatch{tab: "chrome:1:2", interval: time.Millisecond,
		read: func() ([]string, error) { return []string{"a"}, nil },
		selected: func() (bool, error) {
			return false, &cli.ExitError{Code: exitNotFound, Message: "chrome:1:2 closed"}
		}})
	if exitCode(err) != exitNotFound {
		t.Fatalf("closed tab: %v", err)
	}
}

// fakeTarget answers StillFocused true for its first focusedFor checks
// (0 = always), then false.
type fakeTarget struct {
	focusedFor, checks int
	released           bool
}

func (f *fakeTarget) StillFocused() (bool, error) {
	f.checks++
	return f.focusedFor == 0 || f.checks <= f.focusedFor, nil
}

func (f *fakeTarget) Release() { f.released = true }

func TestTabTypeFocusGuardFailsClosedAndRunsPerBurst(t *testing.T) {
	typed := withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	frontWindow = func() (engine.Window, error) { return engine.Window{}, errors.New("no window server") }
	c, _ := pageCtx()
	if err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=x"}); err == nil || len(*typed) != 0 {
		t.Fatalf("guard failed open: %v typed %q", err, *typed)
	}
	// The person switches to another browser window after the first burst.
	calls := 0
	frontWindow = func() (engine.Window, error) {
		calls++
		if calls > 2 { // pre-focus check + first burst check pass
			return engine.Window{ID: "2", App: "Google Chrome", Title: "Mail"}, nil
		}
		return engine.Window{ID: "77", App: "Ghostty"}, nil
	}
	text := strings.Repeat("a", typeBurst+3)
	err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=" + text})
	if exitCode(err) != exitNotFound || !reflect.DeepEqual(*typed, []string{strings.Repeat("a", typeBurst)}) {
		t.Fatalf("mid-typing switch: %v typed %q", err, *typed)
	}
}

func TestTabTypeReleasesTarget(t *testing.T) {
	withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	ft := &fakeTarget{}
	webFocus = func(engine.Window, int, int, int, string, string) (engine.WebTarget, error) { return ft, nil }
	c, _ := pageCtx()
	if err := runTabType(c, []string{"--id=chrome:1:2", "--element=e6", "--text=hello", "--enter"}); err != nil {
		t.Fatal(err)
	}
	if !ft.released || ft.checks != 2 {
		t.Fatalf("released=%v checks=%d (want true, 2: one burst + Enter)", ft.released, ft.checks)
	}
}
