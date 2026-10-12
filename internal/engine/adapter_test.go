package engine

import (
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/engine/core"
)

type mockAdapter struct {
	windows         []Window
	monitors        []Monitor
	moved           map[string]Rect
	focused         string
	listErr         error
	moveErr         error
	focusErr        error
	requestAXErr    error
	requestAXCalled int
	checkAXResult   bool
	checkAXCalled   int

	capturedRect Rect
	capturedPath string
	captureErr   error
	cursorX      int
	cursorY      int
	movedMouseTo *[2]int
	clickedAt    *[2]int
	clickButton  MouseButton
	clickCount   int
	typedText    string
	pressedChord *Chord
	launchedApp  string

	axTexts  map[string][]core.TextItem
	axErr    error
	ocrTexts []core.TextItem
	ocrErr   error
	ocrRect  core.Rect
}

func (m *mockAdapter) ListWindows() ([]Window, error)   { return m.windows, m.listErr }
func (m *mockAdapter) ListMonitors() ([]Monitor, error) { return m.monitors, nil }
func (m *mockAdapter) Move(id string, b Rect) error {
	if m.moveErr != nil {
		return m.moveErr
	}
	if m.moved == nil {
		m.moved = map[string]Rect{}
	}
	m.moved[id] = b
	return nil
}
func (m *mockAdapter) Focus(id string) error {
	if m.focusErr != nil {
		return m.focusErr
	}
	m.focused = id
	return nil
}
func (m *mockAdapter) RequestAccessibility() error {
	m.requestAXCalled++
	return m.requestAXErr
}
func (m *mockAdapter) CheckAccessibility() bool {
	m.checkAXCalled++
	return m.checkAXResult
}
func (m *mockAdapter) CaptureRect(bounds Rect, outPath string) error {
	if m.captureErr != nil {
		return m.captureErr
	}
	m.capturedRect = bounds
	m.capturedPath = outPath
	return nil
}
func (m *mockAdapter) MouseMove(x, y int) error {
	m.movedMouseTo = &[2]int{x, y}
	return nil
}
func (m *mockAdapter) MouseClick(x, y int, button MouseButton, clicks int) error {
	m.clickedAt = &[2]int{x, y}
	m.clickButton = button
	m.clickCount = clicks
	return nil
}
func (m *mockAdapter) CursorPosition() (int, int, error) { return m.cursorX, m.cursorY, nil }
func (m *mockAdapter) TypeText(text string) error {
	m.typedText = text
	return nil
}
func (m *mockAdapter) PressChord(chord Chord) error {
	m.pressedChord = &chord
	return nil
}
func (m *mockAdapter) Launch(app string) error {
	m.launchedApp = app
	return nil
}
func (m *mockAdapter) CheckScreenCapture() bool                  { return true }
func (m *mockAdapter) RequestScreenCapture() error               { return nil }
func (m *mockAdapter) OverlayScale(core.Rect) (float64, float64) { return 1, 1 }
func (m *mockAdapter) AccessibleTexts(id string) ([]core.TextItem, error) {
	return m.axTexts[id], m.axErr
}
func (m *mockAdapter) RecognizeText(b core.Rect) ([]core.TextItem, error) {
	m.ocrRect = b
	return m.ocrTexts, m.ocrErr
}
func (m *mockAdapter) ShowOverlay(core.Rect, *image.RGBA, func() bool) error {
	return core.ErrNotImplemented
}

func sampleWindows() []Window {
	return []Window{
		{ID: "1", Title: "Inbox - Chrome", App: "Google Chrome"},
		{ID: "2", Title: "main.go - VSCode", App: "Code"},
		{ID: "3", Title: "Slack | general", App: "Slack"},
	}
}

func TestApplyFilterByTitleIsPartialAndCaseInsensitive(t *testing.T) {
	out := applyFilter(sampleWindows(), Filter{Title: "ChRoMe"})
	if len(out) != 1 || out[0].ID != "1" {
		t.Fatalf("expected the chrome window, got %+v", out)
	}
}

func TestApplyFilterByAppIsCaseInsensitiveExactMatch(t *testing.T) {
	out := applyFilter(sampleWindows(), Filter{App: "slack"})
	if len(out) != 1 || out[0].ID != "3" {
		t.Fatalf("expected the slack window by exact app match, got %+v", out)
	}

	out = applyFilter(sampleWindows(), Filter{App: "Sla"})
	if len(out) != 0 {
		t.Fatalf("expected no match for app prefix (exact match required), got %+v", out)
	}
}

func TestApplyFilterCombinesTitleAndApp(t *testing.T) {
	ws := append(sampleWindows(), Window{ID: "4", Title: "Dashboards", App: "Google Chrome"})
	out := applyFilter(ws, Filter{Title: "inbox", App: "Google Chrome"})
	if len(out) != 1 || out[0].ID != "1" {
		t.Fatalf("expected only the chrome+inbox window, got %+v", out)
	}
}

func TestListWindowsReturnsAllWhenNoFilter(t *testing.T) {
	a := &mockAdapter{windows: sampleWindows()}
	out, err := listWindowsWith(a, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 windows, got %d", len(out))
	}
}

// TestListWindowsStampsMonitorByCentroid covers BUG-1: each window's
// Monitor field is stamped from the public layer using the same
// 1-indexed IDs that monitors list reports. A window whose centroid
// lies off every monitor must be stamped 0 (the documented "off-screen
// / on no monitor" sentinel).
func TestListWindowsStampsMonitorByCentroid(t *testing.T) {
	a := &mockAdapter{
		// Two windows: one with centroid on monitor 2, one off-screen.
		// Adapter returns id=99 to prove the public layer ignores it
		// and re-IDs via sortMonitors before stamping.
		windows: []Window{
			// centroid (1920+50+400, 0+50+300) = (2370, 350) → monitor 2
			{ID: "a", Title: "right", App: "X", Bounds: Rect{X: 1970, Y: 50, W: 800, H: 600}},
			// centroid is way off-screen → 0
			{ID: "b", Title: "ghost", App: "X", Bounds: Rect{X: -5000, Y: -5000, W: 100, H: 100}},
		},
		monitors: []Monitor{
			{ID: 99, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: 99, X: 1920, Y: 0, Width: 1920, Height: 1080},
		},
	}
	out, err := listWindowsWith(a, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0].Monitor; got != 2 {
		t.Errorf("window on monitor 2: got Monitor=%d, want 2", got)
	}
	if got := out[1].Monitor; got != 0 {
		t.Errorf("off-screen window: got Monitor=%d, want 0 (off-screen sentinel)", got)
	}
}

// TestListWindowsStampsFocused covers BUG-9: the first window (in
// adapter z-order) whose centroid lies on the focused monitor is
// flagged Focused=true, and no other window is. Stamping happens
// before applyFilter so the caller's filter cannot promote a
// non-frontmost window to "focused".
func TestListWindowsStampsFocused(t *testing.T) {
	a := &mockAdapter{
		// z-order top→bottom: w1 (mon 1, not focused), w2 (mon 2, FOCUSED),
		// w3 (mon 2, behind w2). Expect only w2 to be flagged.
		windows: []Window{
			{ID: "w1", Title: "front-left", App: "X", Bounds: Rect{X: 100, Y: 100, W: 800, H: 600}},
			{ID: "w2", Title: "front-right", App: "X", Bounds: Rect{X: 2000, Y: 100, W: 800, H: 600}},
			{ID: "w3", Title: "behind-right", App: "X", Bounds: Rect{X: 2100, Y: 200, W: 800, H: 600}},
		},
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: 2, X: 1920, Y: 0, Width: 1920, Height: 1080, Focused: true},
		},
	}
	out, err := listWindowsWith(a, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Focused {
		t.Errorf("w1 (left monitor) must not be Focused")
	}
	if !out[1].Focused {
		t.Errorf("w2 (frontmost on focused monitor) must be Focused")
	}
	if out[2].Focused {
		t.Errorf("w3 (behind w2 on same monitor) must not be Focused")
	}
}

// TestListWindowsFocusedNoOpWhenNoMonitorFocused guards the linux path
// (and other adapters that don't yet stamp Monitor.Focused): if no
// monitor is flagged Focused, no window is either — we don't
// arbitrarily pick the first listed window.
func TestListWindowsFocusedNoOpWhenNoMonitorFocused(t *testing.T) {
	a := &mockAdapter{
		windows: []Window{
			{ID: "w1", Title: "x", App: "X", Bounds: Rect{X: 100, Y: 100, W: 800, H: 600}},
		},
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		},
	}
	out, err := listWindowsWith(a, Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Focused {
		t.Errorf("Focused must remain false when no monitor is flagged Focused")
	}
}

func TestMoveDelegatesToAdapter(t *testing.T) {
	a := &mockAdapter{windows: sampleWindows()}
	if err := moveWith(a, Match{Title: "chrome"}, Target{Bounds: Rect{X: 10, Y: 20, W: 100, H: 200}}); err != nil {
		t.Fatal(err)
	}
	if got := a.moved["1"]; got != (Rect{X: 10, Y: 20, W: 100, H: 200}) {
		t.Fatalf("unexpected move bounds: %+v", got)
	}
}

func TestMoveReturnsErrNoMatchWhenFilterMatchesNothing(t *testing.T) {
	a := &mockAdapter{windows: sampleWindows()}
	err := moveWith(a, Match{Title: "nope"}, Target{Bounds: Rect{}})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}

func TestFocusDelegatesToAdapter(t *testing.T) {
	a := &mockAdapter{windows: sampleWindows()}
	if err := focusWith(a, Match{App: "Code"}); err != nil {
		t.Fatal(err)
	}
	if a.focused != "2" {
		t.Fatalf("expected window 2 to be focused, got %q", a.focused)
	}
}

func TestFocusReturnsErrNoMatchWhenFilterMatchesNothing(t *testing.T) {
	a := &mockAdapter{windows: sampleWindows()}
	err := focusWith(a, Match{Title: "nope"})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}

// TestErrAccessibilityDeniedSentinelExists guards the contract that the
// macOS AX-denial path returns a typed sentinel rather than an ad-hoc
// error string. Required by docs/specs/01-os-spikes.md "Accessibility
// permission denied on macOS" and requirements.md §11 (the row that
// mandates instructing the user to grant Accessibility access).
func TestErrAccessibilityDeniedSentinelExists(t *testing.T) {
	if ErrAccessibilityDenied == nil {
		t.Fatal("engine.ErrAccessibilityDenied is nil; expected a typed sentinel re-exported from core")
	}
	if !errors.Is(ErrAccessibilityDenied, core.ErrAccessibilityDenied) {
		t.Fatalf("engine.ErrAccessibilityDenied must alias core.ErrAccessibilityDenied; got %v vs %v",
			ErrAccessibilityDenied, core.ErrAccessibilityDenied)
	}
}

// TestErrAccessibilityDeniedMessageGuidesUser pins the user-facing
// message so it stays actionable per requirements.md §11. The smoke
// script (scripts/smoke-darwin.sh) also greps for "Accessibility
// permission denied" in stderr, so this test is the unit-level twin of
// that integration assertion.
func TestErrAccessibilityDeniedMessageGuidesUser(t *testing.T) {
	msg := ErrAccessibilityDenied.Error()
	for _, want := range []string{
		"Accessibility permission denied",
		"System Settings",
		"Privacy & Security",
		"Accessibility",
		// Per spec slice 11: the error message MUST point users at the
		// new opt-in subcommand so the remediation is one copy-paste
		// away. The smoke script (scripts/smoke-darwin.sh) still
		// greps for "Accessibility permission denied" — pinned above.
		"windowctl permissions",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("ErrAccessibilityDenied message missing %q\nfull message: %q", want, msg)
		}
	}
}

// TestMoveSurfacesAccessibilityDeniedFromAdapter ensures the public
// Move path lets the typed sentinel bubble up unchanged when the
// adapter denies the operation. The CGO/AX surface itself is not
// unit-testable on macOS without a granted runtime, so we validate the
// pure-Go plumbing here and the smoke script validates the real path.
func TestMoveSurfacesAccessibilityDeniedFromAdapter(t *testing.T) {
	a := &mockAdapter{windows: sampleWindows(), moveErr: ErrAccessibilityDenied}
	err := moveWith(a, Match{Title: "chrome"}, Target{Bounds: Rect{X: 10, Y: 20, W: 100, H: 200}})
	if !errors.Is(err, ErrAccessibilityDenied) {
		t.Fatalf("expected ErrAccessibilityDenied to propagate through moveWith, got %v", err)
	}
}

// TestFocusSurfacesAccessibilityDeniedFromAdapter — same contract as
// the Move variant, but for the focus path.
func TestFocusSurfacesAccessibilityDeniedFromAdapter(t *testing.T) {
	a := &mockAdapter{windows: sampleWindows(), focusErr: ErrAccessibilityDenied}
	err := focusWith(a, Match{App: "Code"})
	if !errors.Is(err, ErrAccessibilityDenied) {
		t.Fatalf("expected ErrAccessibilityDenied to propagate through focusWith, got %v", err)
	}
}

// TestRequestAccessibilityDelegatesToAdapter pins the contract that the
// public RequestAccessibility() entry point is a thin pass-through to
// the platform adapter. Per spec slice 11 this is how the
// `windowctl permissions` subcommand reaches the macOS AX prompt while
// staying a no-op on linux/windows.
func TestRequestAccessibilityDelegatesToAdapter(t *testing.T) {
	a := &mockAdapter{}
	if err := requestAccessibilityWith(a); err != nil {
		t.Fatalf("expected nil error from delegate, got %v", err)
	}
	if a.requestAXCalled != 1 {
		t.Fatalf("expected adapter.RequestAccessibility to be called exactly once, got %d", a.requestAXCalled)
	}
}

// TestRequestAccessibilitySurfacesDeniedSentinel confirms the AX-denied
// path bubbles up unchanged so the CLI can produce the right exit code
// and message. Mirrors the Move/Focus sentinel-propagation tests.
func TestRequestAccessibilitySurfacesDeniedSentinel(t *testing.T) {
	a := &mockAdapter{requestAXErr: ErrAccessibilityDenied}
	err := requestAccessibilityWith(a)
	if !errors.Is(err, ErrAccessibilityDenied) {
		t.Fatalf("expected ErrAccessibilityDenied to propagate, got %v", err)
	}
}

// TestCheckAccessibilityDelegatesToAdapter pins the contract that the
// public CheckAccessibility() entry point is a thin pass-through to the
// platform adapter. Per spec slice 11 ADDED Requirement, this is the
// read-only sibling of RequestAccessibility — the `--status` flag uses
// it so wrapper scripts can detect AX trust state without triggering
// the macOS system prompt.
func TestCheckAccessibilityDelegatesToAdapter(t *testing.T) {
	a := &mockAdapter{checkAXResult: true}
	got := checkAccessibilityWith(a)
	if !got {
		t.Fatalf("expected true from delegate when adapter returns true, got false")
	}
	if a.checkAXCalled != 1 {
		t.Fatalf("expected adapter.CheckAccessibility to be called exactly once, got %d", a.checkAXCalled)
	}
}

// TestCheckAccessibilitySurfacesDenied confirms the false path bubbles
// up unchanged so the CLI can branch on it.
func TestCheckAccessibilitySurfacesDenied(t *testing.T) {
	a := &mockAdapter{checkAXResult: false}
	if got := checkAccessibilityWith(a); got {
		t.Fatalf("expected false from delegate when adapter returns false, got true")
	}
}

// intPtr is a tiny helper for the Batch tests that need to populate
// the *int pointer fields on BatchEntry.
func intPtr(n int) *int { return &n }

// TestBatchAppliesAllEntriesEvenWhenSomeFail covers the core contract:
// one entry's failure must NOT prevent the next entry from being
// applied. Three entries — coord-mode hit, no-match miss, zone-mode
// hit — and we assert all three results in order, the matching ones
// recorded on the adapter, and the no-match one carrying ErrNoMatch.
func TestBatchAppliesAllEntriesEvenWhenSomeFail(t *testing.T) {
	a := &mockAdapter{
		windows: sampleWindows(),
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		},
	}
	entries := []BatchEntry{
		// 1. Coord-mode hit on Chrome.
		{Title: "chrome", X: intPtr(10), Y: intPtr(20), W: intPtr(100), H: intPtr(200)},
		// 2. Filter that matches nothing — must produce ErrNoMatch
		//    without aborting the loop.
		{App: "Nothing", X: intPtr(0), Y: intPtr(0), W: intPtr(50), H: intPtr(50)},
		// 3. Zone-mode hit on Slack.
		{App: "Slack", Zone: "1A"},
	}
	results := batchWith(a, entries)
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].Err != nil {
		t.Errorf("entry 0 (chrome coord move): expected nil err, got %v", results[0].Err)
	}
	if !errors.Is(results[1].Err, ErrNoMatch) {
		t.Errorf("entry 1 (no-match): expected ErrNoMatch, got %v", results[1].Err)
	}
	if results[2].Err != nil {
		t.Errorf("entry 2 (slack zone move): expected nil err, got %v", results[2].Err)
	}
	if got := a.moved["1"]; got != (Rect{X: 10, Y: 20, W: 100, H: 200}) {
		t.Errorf("chrome (id=1) move bounds: got %+v want {10 20 100 200}", got)
	}
	if _, ok := a.moved["3"]; !ok {
		t.Errorf("slack (id=3) should have been moved by entry 2; moved=%+v", a.moved)
	}
	// Confirm the no-match entry did NOT record a move on any window.
	if len(a.moved) != 2 {
		t.Errorf("expected exactly 2 windows moved (chrome, slack), got %d: %+v", len(a.moved), a.moved)
	}
}

// TestBatchPerEntryValidationContinues feeds entries that fail the
// per-entry validation rules and asserts each fails with a descriptive
// error AND that subsequent entries still run.
func TestBatchPerEntryValidationContinues(t *testing.T) {
	a := &mockAdapter{
		windows: sampleWindows(),
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		},
	}
	entries := []BatchEntry{
		// 0. Missing match (no title, no app).
		{Zone: "1A"},
		// 1. Missing target (no zone, no coords).
		{App: "Slack"},
		// 2. Partial coords — only X / Y, no W / H.
		{App: "Slack", X: intPtr(0), Y: intPtr(0)},
		// 3. Zone + coords together.
		{App: "Slack", Zone: "1A", X: intPtr(0), Y: intPtr(0), W: intPtr(100), H: intPtr(100)},
		// 4. W / H not > 0.
		{App: "Slack", X: intPtr(0), Y: intPtr(0), W: intPtr(0), H: intPtr(100)},
		// 5. Monitor < 1.
		{App: "Slack", Monitor: intPtr(0), Zone: "1A"},
		// 6. Valid entry — must still run after all the failures above.
		{App: "Slack", Zone: "1A"},
	}
	results := batchWith(a, entries)
	if len(results) != len(entries) {
		t.Fatalf("expected %d results, got %d", len(entries), len(results))
	}
	wantSubstr := []string{
		"title or app is required",
		"either zone or x/y/w/h is required",
		"x, y, w and h must all be set",
		"mutually exclusive",
		"w and h must be > 0",
		"monitor must be >= 1",
	}
	for i, want := range wantSubstr {
		if results[i].Err == nil {
			t.Errorf("entry %d: expected validation error containing %q, got nil", i, want)
			continue
		}
		if !strings.Contains(results[i].Err.Error(), want) {
			t.Errorf("entry %d: expected error containing %q, got %q", i, want, results[i].Err.Error())
		}
	}
	if results[6].Err != nil {
		t.Errorf("entry 6 (valid, after all failures): expected nil, got %v", results[6].Err)
	}
	if _, ok := a.moved["3"]; !ok {
		t.Errorf("entry 6 should have moved slack (id=3); moved=%+v", a.moved)
	}
	// None of the validation-failing entries should have produced a move.
	if len(a.moved) != 1 {
		t.Errorf("expected exactly 1 window moved (the final valid entry), got %d: %+v", len(a.moved), a.moved)
	}
}

// TestBatchZoneEntryUsesGivenMonitor pins that an explicit Monitor on
// a zone entry routes through findMonitor (not auto-resolve) and that
// the resulting move bounds reflect the chosen monitor's geometry.
// Zone "1A" is the LEFT HALF of the chosen monitor — picking monitor
// 2 (origin at X=1920) must therefore produce a rect anchored at
// X=1920, not at X=0. That's the load-bearing assertion: the right
// monitor's origin is honored.
func TestBatchZoneEntryUsesGivenMonitor(t *testing.T) {
	a := &mockAdapter{
		windows: sampleWindows(),
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: 2, X: 1920, Y: 0, Width: 2560, Height: 1440},
		},
	}
	entries := []BatchEntry{
		{App: "Slack", Monitor: intPtr(2), Zone: "1A"},
	}
	results := batchWith(a, entries)
	if results[0].Err != nil {
		t.Fatalf("zone-on-monitor-2 entry: expected nil err, got %v", results[0].Err)
	}
	// 1A on monitor 2 = left half, anchored at monitor 2's origin.
	want := Rect{X: 1920, Y: 0, W: 1280, H: 1440}
	if got := a.moved["3"]; got != want {
		t.Fatalf("expected slack moved to monitor 2's left half %+v, got %+v", want, got)
	}
}

// TestCheckAccessibilityDoesNotErrOnSignature is a compile-time pin: by
// returning bool (not (bool, error)), CheckAccessibility states that the
// underlying AXIsProcessTrustedWithOptions(prompt=false) call has no
// failure channel callers can act on. If the signature ever drifts to
// (bool, error), this test will stop compiling.
func TestCheckAccessibilityDoesNotErrOnSignature(t *testing.T) {
	a := &mockAdapter{checkAXResult: true}
	var fn func(Adapter) bool = checkAccessibilityWith
	if !fn(a) {
		t.Fatal("expected true; this test exists for the compile-time signature pin")
	}
}
