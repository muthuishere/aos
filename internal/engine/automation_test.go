package engine

import (
	"strings"
	"testing"
	"time"
)

func newAutomationAdapter() *mockAdapter {
	return &mockAdapter{
		windows: []Window{{ID: "w1", Title: "Inbox - Chrome", App: "Google Chrome", Bounds: Rect{X: 100, Y: 50, W: 800, H: 600}}},
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: 2, X: 1920, Y: 0, Width: 2560, Height: 1440, Focused: true},
		},
	}
}

func TestScreenshotDefaultsToFocusedMonitor(t *testing.T) {
	a := newAutomationAdapter()
	rect, err := screenshotWith(a, ScreenshotOptions{OutPath: "/tmp/x.png"})
	if err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 1920, Y: 0, W: 2560, H: 1440}
	if rect != want || a.capturedRect != want {
		t.Fatalf("got %+v (captured %+v), want %+v", rect, a.capturedRect, want)
	}
}

func TestScreenshotFallsBackToPrimaryWhenNoFocusedMonitor(t *testing.T) {
	a := newAutomationAdapter()
	a.monitors[1].Focused = false
	rect, err := screenshotWith(a, ScreenshotOptions{OutPath: "/tmp/x.png"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Rect{X: 0, Y: 0, W: 1920, H: 1080}); rect != want {
		t.Fatalf("got %+v, want %+v", rect, want)
	}
}

func TestScreenshotExplicitMonitor(t *testing.T) {
	a := newAutomationAdapter()
	id := 1
	rect, err := screenshotWith(a, ScreenshotOptions{Monitor: &id, OutPath: "/tmp/x.png"})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Rect{X: 0, Y: 0, W: 1920, H: 1080}); rect != want {
		t.Fatalf("got %+v, want %+v", rect, want)
	}
}

func TestScreenshotRegionIsMonitorRelativeWhenMonitorSet(t *testing.T) {
	a := newAutomationAdapter()
	id := 2
	rect, err := screenshotWith(a, ScreenshotOptions{
		Monitor: &id,
		Region:  &Rect{X: 10, Y: 20, W: 300, H: 200},
		OutPath: "/tmp/x.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Rect{X: 1930, Y: 20, W: 300, H: 200}); rect != want {
		t.Fatalf("got %+v, want %+v", rect, want)
	}
}

func TestScreenshotRegionIsAbsoluteWithoutMonitor(t *testing.T) {
	a := newAutomationAdapter()
	rect, err := screenshotWith(a, ScreenshotOptions{
		Region:  &Rect{X: 2000, Y: 100, W: 400, H: 300},
		OutPath: "/tmp/x.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Rect{X: 2000, Y: 100, W: 400, H: 300}); rect != want {
		t.Fatalf("got %+v, want %+v", rect, want)
	}
}

func TestScreenshotWindowMatchUsesWindowBounds(t *testing.T) {
	a := newAutomationAdapter()
	rect, err := screenshotWith(a, ScreenshotOptions{
		Match:   &Match{Title: "inbox"},
		OutPath: "/tmp/x.png",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (Rect{X: 100, Y: 50, W: 800, H: 600}); rect != want {
		t.Fatalf("got %+v, want %+v", rect, want)
	}
}

func TestScreenshotRejectsMatchPlusRegion(t *testing.T) {
	a := newAutomationAdapter()
	_, err := screenshotWith(a, ScreenshotOptions{
		Match:   &Match{Title: "inbox"},
		Region:  &Rect{W: 10, H: 10},
		OutPath: "/tmp/x.png",
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

func TestScreenshotRejectsEmptyOutPathAndBadRegion(t *testing.T) {
	a := newAutomationAdapter()
	if _, err := screenshotWith(a, ScreenshotOptions{}); err == nil {
		t.Fatal("expected error for missing out path")
	}
	if _, err := screenshotWith(a, ScreenshotOptions{Region: &Rect{W: 0, H: 10}, OutPath: "/tmp/x.png"}); err == nil {
		t.Fatal("expected error for zero-width region")
	}
}

func TestMouseMoveMonitorRelative(t *testing.T) {
	a := newAutomationAdapter()
	id := 2
	if err := mouseMoveWith(a, &id, 100, 200); err != nil {
		t.Fatal(err)
	}
	if want := [2]int{2020, 200}; *a.movedMouseTo != want {
		t.Fatalf("got %v, want %v", *a.movedMouseTo, want)
	}
}

func TestMouseClickAtCurrentPositionWhenNoCoords(t *testing.T) {
	a := newAutomationAdapter()
	a.cursorX, a.cursorY = 55, 66
	if err := mouseClickWith(a, ClickOptions{Button: MouseRight, Double: true}); err != nil {
		t.Fatal(err)
	}
	if want := [2]int{55, 66}; *a.clickedAt != want {
		t.Fatalf("clicked at %v, want %v", *a.clickedAt, want)
	}
	if a.clickButton != MouseRight || a.clickCount != 2 {
		t.Fatalf("got button=%v clicks=%d, want right double", a.clickButton, a.clickCount)
	}
}

func TestMouseClickRejectsHalfCoords(t *testing.T) {
	a := newAutomationAdapter()
	x := 10
	if err := mouseClickWith(a, ClickOptions{X: &x}); err == nil {
		t.Fatal("expected error for x without y")
	}
}

func TestMouseClickMonitorRelativeCoords(t *testing.T) {
	a := newAutomationAdapter()
	id, x, y := 2, 30, 40
	if err := mouseClickWith(a, ClickOptions{Monitor: &id, X: &x, Y: &y}); err != nil {
		t.Fatal(err)
	}
	if want := [2]int{1950, 40}; *a.clickedAt != want {
		t.Fatalf("clicked at %v, want %v", *a.clickedAt, want)
	}
	if a.clickCount != 1 || a.clickButton != MouseLeft {
		t.Fatalf("got button=%v clicks=%d, want left single", a.clickButton, a.clickCount)
	}
}

func TestParseChordModifiersAndAliases(t *testing.T) {
	chord, err := ParseChord("cmd+shift+s")
	if err != nil {
		t.Fatal(err)
	}
	if !chord.Cmd || !chord.Shift || chord.Ctrl || chord.Alt || chord.Key != "s" {
		t.Fatalf("got %+v", chord)
	}
	chord, err = ParseChord("Option+Return")
	if err != nil {
		t.Fatal(err)
	}
	if !chord.Alt || chord.Key != "enter" {
		t.Fatalf("got %+v", chord)
	}
	chord, err = ParseChord("win+pgdn")
	if err != nil {
		t.Fatal(err)
	}
	if !chord.Cmd || chord.Key != "pagedown" {
		t.Fatalf("got %+v", chord)
	}
}

func TestParseChordBareAndFunctionKeys(t *testing.T) {
	for combo, key := range map[string]string{"enter": "enter", "f11": "f11", "escape": "esc", "/": "/"} {
		chord, err := ParseChord(combo)
		if err != nil {
			t.Fatalf("%s: %v", combo, err)
		}
		if chord.Key != key || chord.Cmd || chord.Ctrl || chord.Alt || chord.Shift {
			t.Fatalf("%s: got %+v, want bare key %q", combo, chord, key)
		}
	}
}

func TestParseChordRejectsUnknownTokens(t *testing.T) {
	for _, combo := range []string{"", "hyper+s", "cmd+notakey", "f13"} {
		if _, err := ParseChord(combo); err == nil {
			t.Fatalf("expected error for %q", combo)
		}
	}
}

func TestWaitForWindowReturnsOnceWindowAppears(t *testing.T) {
	a := newAutomationAdapter()
	a.windows = nil
	oldSleep, oldInterval := sleepFn, waitPollInterval
	waitPollInterval = 0
	polls := 0
	sleepFn = func(time.Duration) {
		polls++
		if polls == 3 {
			a.windows = []Window{{ID: "w9", Title: "TextEdit Doc", App: "TextEdit"}}
		}
	}
	defer func() { sleepFn, waitPollInterval = oldSleep, oldInterval }()

	w, err := waitForWindowWith(a, Filter{App: "textedit"}, 5000)
	if err != nil {
		t.Fatal(err)
	}
	if w.ID != "w9" {
		t.Fatalf("got %+v", w)
	}
}

func TestWaitForWindowTimesOut(t *testing.T) {
	a := newAutomationAdapter()
	a.windows = nil
	oldSleep, oldNow, oldInterval := sleepFn, nowFn, waitPollInterval
	waitPollInterval = 0
	now := time.Unix(0, 0)
	nowFn = func() time.Time { return now }
	sleepFn = func(time.Duration) { now = now.Add(time.Second) }
	defer func() { sleepFn, nowFn, waitPollInterval = oldSleep, oldNow, oldInterval }()

	_, err := waitForWindowWith(a, Filter{Title: "never"}, 2000)
	if err == nil || !strings.Contains(err.Error(), "within 2000ms") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestTypeIntoVerifiesFocusBeforeTyping(t *testing.T) {
	a := newAutomationAdapter()
	// Window centroid (500,350) is on monitor 1; make monitor 1 the
	// focused monitor so stampFocused marks the window Focused.
	a.monitors[0].Focused = true
	a.monitors[1].Focused = false
	if err := typeIntoWith(a, Match{App: "google chrome"}, "hello"); err != nil {
		t.Fatal(err)
	}
	if a.focused != "w1" {
		t.Fatalf("focus was not called on the matched window, got %q", a.focused)
	}
	if a.typedText != "hello" {
		t.Fatalf("typed %q", a.typedText)
	}
}

func TestTypeIntoRefusesWhenFocusNeverLands(t *testing.T) {
	a := newAutomationAdapter()
	// Monitor 2 holds focus but the window sits on monitor 1: the
	// window never reports Focused, so the guard must refuse to type.
	oldSleep, oldNow, oldInterval := sleepFn, nowFn, waitPollInterval
	waitPollInterval = 0
	now := time.Unix(0, 0)
	nowFn = func() time.Time { return now }
	sleepFn = func(time.Duration) { now = now.Add(time.Second) }
	defer func() { sleepFn, nowFn, waitPollInterval = oldSleep, oldNow, oldInterval }()

	err := typeIntoWith(a, Match{App: "google chrome"}, "hello")
	if err == nil || !strings.Contains(err.Error(), "input NOT sent") {
		t.Fatalf("expected focus-verification refusal, got %v", err)
	}
	if a.typedText != "" {
		t.Fatalf("guard failed: text was typed anyway: %q", a.typedText)
	}
}

func TestTypeIntoSkipsVerificationWhenPlatformCannotReportFocus(t *testing.T) {
	a := newAutomationAdapter()
	// No monitor reports Focused (linux today): trust Focus() and type.
	a.monitors[1].Focused = false
	if err := typeIntoWith(a, Match{App: "google chrome"}, "hi"); err != nil {
		t.Fatal(err)
	}
	if a.typedText != "hi" {
		t.Fatalf("typed %q", a.typedText)
	}
}

func TestPressKeyIntoUsesSameGuard(t *testing.T) {
	a := newAutomationAdapter()
	a.monitors[0].Focused = true
	a.monitors[1].Focused = false
	if err := pressKeyIntoWith(a, Match{App: "google chrome"}, "cmd+s"); err != nil {
		t.Fatal(err)
	}
	if a.pressedChord == nil || !a.pressedChord.Cmd || a.pressedChord.Key != "s" {
		t.Fatalf("got %+v", a.pressedChord)
	}
}

func TestWaitForWindowRequiresFilterAndPositiveTimeout(t *testing.T) {
	a := newAutomationAdapter()
	if _, err := waitForWindowWith(a, Filter{}, 1000); err == nil {
		t.Fatal("expected error for empty filter")
	}
	if _, err := waitForWindowWith(a, Filter{Title: "x"}, 0); err == nil {
		t.Fatal("expected error for zero timeout")
	}
}
