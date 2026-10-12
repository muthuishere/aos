// Package windowctl is the public Go API for the windowctl cross-platform
// window manager. It re-exports the OS-agnostic types from internal/core and
// delegates all operations to the platform adapter selected at build time.
package engine

import (
	"errors"
	"strings"

	"github.com/muthuishere/aos/internal/engine/core"
)

type (
	Window      = core.Window
	Monitor     = core.Monitor
	Rect        = core.Rect
	Filter      = core.Filter
	Match       = core.Match
	Target      = core.Target
	Adapter     = core.Adapter
	MouseButton = core.MouseButton
	Chord       = core.Chord
)

const (
	MouseLeft   = core.MouseLeft
	MouseRight  = core.MouseRight
	MouseMiddle = core.MouseMiddle
)

var (
	ErrNotImplemented      = core.ErrNotImplemented
	ErrNoMatch             = core.ErrNoMatch
	ErrAccessibilityDenied = core.ErrAccessibilityDenied
	ErrScreenCaptureDenied = core.ErrScreenCaptureDenied
)

var defaultAdapter Adapter = newPlatformAdapter()

func ListWindows(filter Filter) ([]Window, error) {
	return listWindowsWith(defaultAdapter, filter)
}

func ListMonitors() ([]Monitor, error) {
	return listMonitorsWith(defaultAdapter)
}

func listMonitorsWith(a Adapter) ([]Monitor, error) {
	ms, err := a.ListMonitors()
	if err != nil {
		return nil, err
	}
	return sortMonitors(ms), nil
}

func Move(match Match, target Target) error {
	return moveWith(defaultAdapter, match, target)
}

func Focus(match Match) error {
	return focusWith(defaultAdapter, match)
}

// Resize changes the matched window's width and height while keeping
// its current X/Y position. It's a thin convenience over Move that
// avoids forcing callers to first list the window just to read its
// current top-left.
func Resize(match Match, width, height int) error {
	return resizeWith(defaultAdapter, match, width, height)
}

func resizeWith(a Adapter, match Match, width, height int) error {
	w, err := matchOne(a, match)
	if err != nil {
		return err
	}
	return a.Move(w.ID, Rect{X: w.Bounds.X, Y: w.Bounds.Y, W: width, H: height})
}

// RequestAccessibility asks the platform adapter to verify (and on
// macOS, prompt for) the privileges needed for Move / Focus. It is
// the public entry point invoked by the `windowctl permissions`
// subcommand; see docs/specs/11-permissions-subcommand.md.
//
// On macOS this triggers the AX trust check with prompt=true, which
// causes the system to display its "wants to control your computer"
// dialog the first time it is called from a given parent process
// (TCC is keyed per parent process). Returns ErrAccessibilityDenied
// when the post-call trust state is still false.
//
// On Linux and Windows this is a no-op and always returns nil.
func RequestAccessibility() error {
	return requestAccessibilityWith(defaultAdapter)
}

func requestAccessibilityWith(a Adapter) error {
	return a.RequestAccessibility()
}

// CheckAccessibility is the read-only sibling of RequestAccessibility.
// It returns the current AX trust state without triggering the macOS
// system dialog — the contract that lets `windowctl permissions
// --status` be a script-friendly detection path. See
// docs/specs/11-permissions-subcommand.md ADDED Requirement.
//
// On macOS: calls AXIsProcessTrustedWithOptions with
// kAXTrustedCheckOptionPrompt = false; returns true if the process is
// currently trusted, false otherwise.
//
// On Linux and Windows: no-op that always returns true.
func CheckAccessibility() bool {
	return checkAccessibilityWith(defaultAdapter)
}

func checkAccessibilityWith(a Adapter) bool {
	return a.CheckAccessibility()
}

// RequestScreenCapture / CheckScreenCapture mirror the Accessibility
// pair for the macOS Screen Recording permission (the separate TCC
// gate Screenshot needs). Request may surface the system prompt once
// and returns ErrScreenCaptureDenied when still denied; Check never
// prompts. Both are no-ops (nil / true) on linux and windows.
func RequestScreenCapture() error {
	return defaultAdapter.RequestScreenCapture()
}

func CheckScreenCapture() bool {
	return defaultAdapter.CheckScreenCapture()
}

// MoveZone moves the window matched by `match` into the given zone on the
// resolved monitor. monitorID is optional: when nil, the monitor is
// auto-resolved as the one containing the majority of the window's area
// (FR-MOV-03). zoneStr accepts enum (e.g. "2B") or split (e.g. "3:1") form.
func MoveZone(match Match, monitorID *int, zoneStr string) error {
	return moveZoneWith(defaultAdapter, match, monitorID, zoneStr)
}

// MoveCoords moves the matched window using raw coordinates. When monitorID
// is nil, bounds are interpreted as absolute global coordinates; when
// monitorID is non-nil, bounds are interpreted as relative to that
// monitor's origin (FR-MOV-02).
func MoveCoords(match Match, monitorID *int, bounds Rect) error {
	return moveCoordsWith(defaultAdapter, match, monitorID, bounds)
}

func moveZoneWith(a Adapter, match Match, monitorID *int, zoneStr string) error {
	zone, err := ParseZone(zoneStr)
	if err != nil {
		return err
	}
	w, err := matchOne(a, match)
	if err != nil {
		return err
	}
	monitors, err := listMonitorsWith(a)
	if err != nil {
		return err
	}
	var mon Monitor
	if monitorID != nil {
		mon, err = findMonitor(monitors, *monitorID)
		if err != nil {
			return err
		}
	} else {
		mon, err = resolveCurrentMonitor(w, monitors)
		if err != nil {
			return err
		}
	}
	return a.Move(w.ID, zone.Rect(mon))
}

func moveCoordsWith(a Adapter, match Match, monitorID *int, bounds Rect) error {
	w, err := matchOne(a, match)
	if err != nil {
		return err
	}
	final := bounds
	if monitorID != nil {
		monitors, err := listMonitorsWith(a)
		if err != nil {
			return err
		}
		mon, err := findMonitor(monitors, *monitorID)
		if err != nil {
			return err
		}
		final = Rect{
			X: mon.X + bounds.X,
			Y: mon.Y + bounds.Y,
			W: bounds.W,
			H: bounds.H,
		}
	}
	return a.Move(w.ID, final)
}

func listWindowsWith(a Adapter, filter Filter) ([]Window, error) {
	ws, err := a.ListWindows()
	if err != nil {
		return nil, err
	}
	// Stamp Monitor + Focused on the *unfiltered* z-ordered list so
	// (a) Window.Monitor uses the same 1-indexed IDs that monitors
	// list reports (BUG-1), and (b) Window.Focused refers to the true
	// frontmost window on the focused monitor — independent of which
	// windows the caller's filter happens to keep (BUG-9).
	monitors, mErr := listMonitorsWith(a)
	if mErr == nil {
		stampMonitor(ws, monitors)
		stampFocused(ws, monitors)
	}
	return applyFilter(ws, filter), nil
}

// stampMonitor sets each window's Monitor field to the 1-indexed ID of
// the monitor containing its centroid, or 0 if its centroid is off
// every monitor. Mutates ws in place.
func stampMonitor(ws []Window, ms []Monitor) {
	for i := range ws {
		ws[i].Monitor = monitorIDForCentroid(ws[i].Bounds, ms)
	}
}

// stampFocused sets Focused=true on the first window (in the adapter's
// z-order, frontmost first on darwin/windows) whose centroid lies on
// the monitor flagged Focused. We rely on the adapter-stamped
// Monitor.Focused as the source of truth for "which display has the
// key window" rather than adding an OS-specific frontmost-window probe
// to the public layer; the first-in-z-order window on that display is
// then by definition the focused window.
//
// No-op if no monitor is flagged Focused (e.g. linux adapter today) or
// if no listed window's centroid lands on it.
func stampFocused(ws []Window, ms []Monitor) {
	focusedID := 0
	for _, m := range ms {
		if m.Focused {
			focusedID = m.ID
			break
		}
	}
	if focusedID == 0 {
		return
	}
	for i := range ws {
		if ws[i].Monitor == focusedID {
			ws[i].Focused = true
			return
		}
	}
}

func moveWith(a Adapter, match Match, target Target) error {
	w, err := matchOne(a, match)
	if err != nil {
		return err
	}
	return a.Move(w.ID, target.Bounds)
}

func focusWith(a Adapter, match Match) error {
	w, err := matchOne(a, match)
	if err != nil {
		return err
	}
	return a.Focus(w.ID)
}

func matchOne(a Adapter, match Match) (Window, error) {
	ws, err := a.ListWindows()
	if err != nil {
		return Window{}, err
	}
	matches := applyFilter(ws, Filter{Title: match.Title, App: match.App})
	if len(matches) == 0 {
		return Window{}, ErrNoMatch
	}
	return matches[0], nil
}

// BatchEntry describes a single move to apply as part of a Batch call.
// Either Zone OR all four of X/Y/W/H must be set (mutually exclusive);
// at least one of Title or App must be set. Monitor is optional and,
// when set, must be >= 1. In coord mode with Monitor set, X/Y are
// interpreted as monitor-relative (matching MoveCoords).
//
// Sample JSON shape (one element of the array `windowctl batch` reads):
//
//	{"app": "Ghostty", "monitor": 2, "x": 0, "y": 25, "w": 1920, "h": 1055}
//	{"app": "Activity Monitor", "monitor": 1, "zone": "2A"}
//	{"title": "Inbox", "x": 100, "y": 100, "w": 800, "h": 600}
type BatchEntry struct {
	Title   string `json:"title,omitempty"`
	App     string `json:"app,omitempty"`
	Monitor *int   `json:"monitor,omitempty"`
	Zone    string `json:"zone,omitempty"`
	X       *int   `json:"x,omitempty"`
	Y       *int   `json:"y,omitempty"`
	W       *int   `json:"w,omitempty"`
	H       *int   `json:"h,omitempty"`
}

// BatchResult pairs a BatchEntry with the error (if any) that was
// observed when applying it. Err is nil on success.
type BatchResult struct {
	Entry BatchEntry
	Err   error
}

// Batch applies each entry in `entries` as a Move, sequentially, never
// aborting when one entry fails. The returned slice is one-to-one with
// `entries` (same order, same length); inspect each result's Err to
// determine per-entry success.
func Batch(entries []BatchEntry) []BatchResult {
	return batchWith(defaultAdapter, entries)
}

func batchWith(a Adapter, entries []BatchEntry) []BatchResult {
	results := make([]BatchResult, len(entries))
	for i, e := range entries {
		results[i] = BatchResult{Entry: e, Err: applyBatchEntry(a, e)}
	}
	return results
}

// applyBatchEntry validates a single BatchEntry and dispatches to the
// matching *With helper. Validation mirrors moveCmd in cmd/windowctl:
// must have Title or App; must have EITHER Zone OR all four of X/Y/W/H;
// in coord mode W and H must be > 0; Monitor (if set) must be >= 1.
func applyBatchEntry(a Adapter, e BatchEntry) error {
	if e.Title == "" && e.App == "" {
		return errors.New("batch entry: title or app is required")
	}
	hasCoord := e.X != nil || e.Y != nil || e.W != nil || e.H != nil
	hasZone := e.Zone != ""
	if hasZone && hasCoord {
		return errors.New("batch entry: zone and x/y/w/h are mutually exclusive")
	}
	if !hasZone && !hasCoord {
		return errors.New("batch entry: either zone or x/y/w/h is required")
	}
	if hasCoord {
		if e.X == nil || e.Y == nil || e.W == nil || e.H == nil {
			return errors.New("batch entry: x, y, w and h must all be set in coord mode (partial coords are not supported)")
		}
		if *e.W <= 0 || *e.H <= 0 {
			return errors.New("batch entry: w and h must be > 0 in coord mode")
		}
	}
	if e.Monitor != nil && *e.Monitor < 1 {
		return errors.New("batch entry: monitor must be >= 1")
	}
	match := Match{Title: e.Title, App: e.App}
	if hasZone {
		return moveZoneWith(a, match, e.Monitor, e.Zone)
	}
	return moveCoordsWith(a, match, e.Monitor, Rect{X: *e.X, Y: *e.Y, W: *e.W, H: *e.H})
}

func applyFilter(ws []Window, f Filter) []Window {
	if f.Title == "" && f.App == "" {
		return ws
	}
	out := make([]Window, 0, len(ws))
	for _, w := range ws {
		if f.Title != "" && !strings.Contains(strings.ToLower(w.Title), strings.ToLower(f.Title)) {
			continue
		}
		if f.App != "" && !strings.EqualFold(w.App, f.App) {
			continue
		}
		out = append(out, w)
	}
	return out
}
