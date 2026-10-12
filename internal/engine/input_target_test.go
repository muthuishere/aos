package engine

import (
	"errors"
	"strings"
	"testing"
)

// targetedMock is mockAdapter plus focus-free input and a text field.
type targetedMock struct {
	*mockAdapter
	postedTo   string
	postedText string
	postedKey  *Chord
	field      string
	readable   bool
	dropPosts  bool // the app ignores posted events
	pressedAt  *[2]int
}

func (t *targetedMock) TypeTextTo(w Window, text string) error {
	t.postedTo, t.postedText = w.ID, text
	if !t.dropPosts {
		t.field += text
	}
	return nil
}
func (t *targetedMock) PressChordTo(w Window, c Chord) error {
	t.postedTo, t.postedKey = w.ID, &c
	return nil
}
func (t *targetedMock) FocusedText(Window) (string, bool, error) {
	return t.field, t.readable, nil
}
func (t *targetedMock) PressElementAt(w Window, x, y int) error {
	t.pressedAt = &[2]int{x, y}
	return nil
}

func newTargeted() *targetedMock {
	m := newAutomationAdapter()
	m.windows = []Window{
		{ID: "tip", Title: "", App: "TextEdit", Bounds: Rect{X: 0, Y: 120, W: 68, H: 22}},
		{ID: "doc", Title: "notes.txt", App: "TextEdit", Bounds: Rect{X: 100, Y: 100, W: 700, H: 450}},
		{ID: "mine", Title: "Owner doc", App: "Code", Bounds: Rect{X: 2000, Y: 50, W: 900, H: 700}},
	}
	return &targetedMock{mockAdapter: m, readable: true}
}

func TestChooseRoute(t *testing.T) {
	w := Window{App: "TextEdit"}
	chrome := Window{App: "Google Chrome"}
	ctrlS := Chord{Ctrl: true, Key: "s"}
	enter := Chord{Key: "enter"}
	cases := []struct {
		name       string
		goos       string
		targeted   bool
		w          Window
		chord      *Chord
		opts       InputOptions
		wantRoute  string
		wantReason string
	}{
		{"post by default", "darwin", true, w, nil, InputOptions{}, RoutePost, ""},
		{"--focus forces focus", "darwin", true, w, nil, InputOptions{Focus: true}, RouteFocus, "--focus"},
		{"no targeted input", "linux", false, w, nil, InputOptions{}, RouteFocus, "no focus-free"},
		{"chromium on mac", "darwin", true, chrome, nil, InputOptions{}, RouteFocus, "ignores posted"},
		{"chrome on windows posts", "windows", true, chrome, nil, InputOptions{}, RoutePost, ""},
		{"uwp on windows", "windows", true, Window{App: "ApplicationFrameHost"}, nil, InputOptions{}, RouteFocus, "ignores posted"},
		{"modifier chord on windows", "windows", true, w, &ctrlS, InputOptions{}, RouteFocus, "modifier"},
		{"plain key on windows posts", "windows", true, w, &enter, InputOptions{}, RoutePost, ""},
		{"modifier chord on mac posts", "darwin", true, w, &ctrlS, InputOptions{}, RoutePost, ""},
	}
	for _, c := range cases {
		route, reason := chooseRoute(c.goos, c.targeted, c.w, c.chord, c.opts)
		if route != c.wantRoute || !strings.Contains(reason, c.wantReason) {
			t.Errorf("%s: got %s (%q), want %s (%q)", c.name, route, reason, c.wantRoute, c.wantReason)
		}
	}
}

func TestTypeToPostsToTargetNotFrontmost(t *testing.T) {
	a := newTargeted()
	res, err := typeToWith(a, "darwin", Match{App: "TextEdit"}, "hello", InputOptions{Verify: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Route != RoutePost || !res.Verified || a.postedTo != "doc" {
		t.Fatalf("res %+v posted to %q", res, a.postedTo)
	}
	if a.typedText != "" || a.focused != "" {
		t.Fatalf("focus path used: typed %q focused %q", a.typedText, a.focused)
	}
}

func TestTypeToVerifyFailsWhenTextDoesNotArrive(t *testing.T) {
	a := newTargeted()
	a.dropPosts = true
	_, err := typeToWith(a, "darwin", Match{App: "TextEdit"}, "hello", InputOptions{Verify: true})
	if !errors.Is(err, ErrNotVerified) || !strings.Contains(err.Error(), "--focus") {
		t.Fatalf("want ErrNotVerified with a --focus hint, got %v", err)
	}
}

func TestTypeToVerifyRefusesUnreadableTarget(t *testing.T) {
	a := newTargeted()
	a.readable = false
	_, err := typeToWith(a, "darwin", Match{App: "TextEdit"}, "hello", InputOptions{Verify: true})
	if !errors.Is(err, ErrNotVerified) || a.postedText != "" {
		t.Fatalf("must refuse before sending: err %v, posted %q", err, a.postedText)
	}
}

func TestTypeToNoMatchNeverTypes(t *testing.T) {
	a := newTargeted()
	_, err := typeToWith(a, "darwin", Match{App: "Nope"}, "hello", InputOptions{})
	if !errors.Is(err, ErrNoMatch) || a.postedText != "" || a.typedText != "" {
		t.Fatalf("err %v posted %q typed %q", err, a.postedText, a.typedText)
	}
}

func TestPressToFocusRouteHandsFocusBack(t *testing.T) {
	a := newTargeted()
	a.monitors = []Monitor{{ID: 1, X: 0, Y: 0, Width: 4000, Height: 1080, Focused: true}}
	a.windows = []Window{
		{ID: "mine", Title: "Owner doc", App: "Code", Bounds: Rect{X: 2000, Y: 50, W: 900, H: 700}},
		{ID: "doc", Title: "notes.txt", App: "TextEdit", Bounds: Rect{X: 100, Y: 100, W: 700, H: 450}},
	}
	a.focused = "mine"
	f := &focusTracker{targetedMock: a}
	res, err := pressToWith(f, "darwin", Match{App: "TextEdit"}, Chord{Key: "enter"}, InputOptions{Focus: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Route != RouteFocus || a.pressedChord == nil || a.postedKey != nil {
		t.Fatalf("res %+v pressed %v posted %v", res, a.pressedChord, a.postedKey)
	}
	if a.focused != "mine" {
		t.Fatalf("focus not handed back to the owner's window: %q", a.focused)
	}
}

// focusTracker lists the focused window first (topmost), as a real window
// server does, so ensureFocused can verify and focus can be handed back.
type focusTracker struct{ *targetedMock }

func (f *focusTracker) ListWindows() ([]Window, error) {
	var first, rest []Window
	for _, w := range f.windows {
		if w.ID == f.focused {
			first = append(first, w)
		} else {
			rest = append(rest, w)
		}
	}
	return append(first, rest...), nil
}

func TestTextArrived(t *testing.T) {
	cases := []struct {
		before, after, text string
		want                bool
	}{
		{"", "hello", "hello", true},
		{"", "Hello", "hello", true}, // auto-capitalised
		{"hello", "hello", "hello", false},
		{"hello", "hello hello", "hello", true},
		{"a", "a\r\nb", "\nb", true},
		{"", "xyz", "hello", false},
	}
	for _, c := range cases {
		if got := textArrived(c.before, c.after, c.text); got != c.want {
			t.Errorf("textArrived(%q,%q,%q) = %v", c.before, c.after, c.text, got)
		}
	}
}

func TestPickInputWindowSkipsHelpers(t *testing.T) {
	ws := []Window{
		{ID: "tip", Bounds: Rect{W: 68, H: 22}},
		{ID: "strip", Bounds: Rect{W: 1700, H: 45}},
		{ID: "untitled", Bounds: Rect{W: 500, H: 400}},
		{ID: "doc", Title: "a.txt", Bounds: Rect{W: 500, H: 400}},
	}
	if got := pickInputWindow(ws).ID; got != "doc" {
		t.Fatalf("got %s", got)
	}
	if got := pickInputWindow(ws[:3]).ID; got != "untitled" {
		t.Fatalf("got %s", got)
	}
	if got := pickInputWindow(ws[:1]).ID; got != "tip" {
		t.Fatalf("got %s", got)
	}
}

func TestClickFoundPressesWithoutPointer(t *testing.T) {
	a := newTargeted()
	a.axTexts = map[string][]TextItem{"doc": {{Text: "Save", Role: "button", Source: "accessibility", Bounds: Rect{X: 200, Y: 200, W: 60, H: 20}}}}
	res, err := clickFoundWith(a, FindOptions{Match: Match{App: "TextEdit", Title: "notes"}, Text: "Save"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Route != "press" || a.pressedAt == nil || a.clickedAt != nil || a.focused != "" {
		t.Fatalf("res %+v pressed %v clicked %v focused %q", res, a.pressedAt, a.clickedAt, a.focused)
	}
	if *a.pressedAt != [2]int{230, 210} {
		t.Fatalf("pressed at %v", *a.pressedAt)
	}
}

func TestFindIgnoresBidiMarks(t *testing.T) {
	if tier, _ := matchTier("‎42", "42"); tier != 0 {
		t.Fatalf("tier %d", tier)
	}
}
