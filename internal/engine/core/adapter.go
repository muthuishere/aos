package core

import (
	"errors"
	"image"
)

// Sentinel errors are stripped of any "windowctl:" prefix so the CLI
// can add it once at print time. Wrapping these from elsewhere in the
// codebase (via fmt.Errorf("%w: ...", core.ErrNoMatch, ...)) therefore
// also prints clean.
var ErrNotImplemented = errors.New("operation not implemented on this platform")

var ErrNoMatch = errors.New("no window matched the filter")

// ErrWebStale is returned by WebAct when the element at an index is no
// longer the one the caller read: the page changed.
var ErrWebStale = errors.New("the page changed since it was read")

// ErrWebFocus is returned by WebAct(WebFocus) when the browser's focused
// element is not the target after focusing it.
var ErrWebFocus = errors.New("focus did not land on the field")

// ErrAccessibilityDenied is returned by the macOS adapter when a Move
// or Focus call is attempted while the running process is not trusted
// by the Accessibility (AX) subsystem. The message instructs the user
// how to grant access, per requirements.md §11. Other adapters may
// reuse this sentinel if a similar concept ever applies.
//
// Per docs/specs/11-permissions-subcommand.md the message also points
// users at the `windowctl permissions` opt-in subcommand so the
// remediation is one copy-paste away. The smoke harness still greps
// for the leading "Accessibility permission denied" substring.
var ErrAccessibilityDenied = errors.New("Accessibility permission denied — run 'windowctl permissions' to grant, or grant manually in System Settings → Privacy & Security → Accessibility, then re-run")

// ErrScreenCaptureDenied is the Screen Recording sibling of
// ErrAccessibilityDenied: returned by the macOS adapter when a Capture
// call is attempted while the process lacks the separate Screen
// Recording TCC permission (Accessibility trust does NOT imply it).
// Same remediation pattern: `windowctl permissions` is the discoverable
// opt-in, manual grant is the fallback.
var ErrScreenCaptureDenied = errors.New("Screen Recording permission denied — run 'windowctl permissions --screen' to grant, or grant manually in System Settings → Privacy & Security → Screen Recording, then re-run")

type Adapter interface {
	ListWindows() ([]Window, error)
	ListMonitors() ([]Monitor, error)
	Move(windowID string, bounds Rect) error
	Focus(windowID string) error
	// RequestAccessibility asks the platform whether the current
	// process holds the privileges needed for Move / Focus. On macOS
	// it triggers the AX trust check WITH prompt enabled, surfacing
	// the system "wants to control your computer" dialog the first
	// time it is called from a given parent process. On Linux and
	// Windows it is a no-op that returns nil — kept on the interface
	// so the CLI surface is symmetric across platforms.
	//
	// Returns ErrAccessibilityDenied when the platform reports the
	// process is not trusted. Returns nil on success or on platforms
	// where no permission setup is required.
	RequestAccessibility() error
	// CheckAccessibility is the read-only sibling of
	// RequestAccessibility. On macOS it inspects the current AX
	// trust state via AXIsProcessTrustedWithOptions with
	// kAXTrustedCheckOptionPrompt = false — never triggering the
	// system dialog. On Linux and Windows it is a no-op that returns
	// true (no comparable per-process trust gate exists on those
	// platforms).
	//
	// The signature returns bool (not (bool, error)) deliberately:
	// the underlying macOS call has no actionable failure channel,
	// and any internal allocation failure inside the C helper
	// collapses to the safe-failing answer (false / denied). See
	// docs/specs/11-permissions-subcommand.md ADDED Requirement.
	CheckAccessibility() bool

	// CaptureRect writes a PNG of the given virtual-desktop rect (the
	// same global point coordinate space Move and ListMonitors use) to
	// outPath. Adapters receive a fully resolved rect — monitor /
	// window / region resolution happens in the public package. The
	// written image is normalized to point dimensions (1 image pixel
	// == 1 coordinate point) even on HiDPI/retina displays, so
	// coordinates read off the image can be fed straight back into
	// MouseClick. Returns ErrScreenCaptureDenied on macOS when the
	// Screen Recording permission is missing.
	CaptureRect(bounds Rect, outPath string) error

	// MouseMove warps the OS cursor to the given virtual-desktop
	// point. MouseClick moves there first, then presses and releases
	// `button` `clicks` times (2 = double-click). CursorPosition
	// reports where the cursor currently is, in the same space.
	MouseMove(x, y int) error
	MouseClick(x, y int, button MouseButton, clicks int) error
	CursorPosition() (int, int, error)

	// TypeText injects the literal unicode string into the focused
	// window (no keycode mapping — IME-safe on darwin/windows via
	// unicode key events, xdotool type on linux). PressChord presses a
	// single key with modifiers held (e.g. cmd+shift+s). Both require
	// Accessibility trust on macOS and return ErrAccessibilityDenied
	// without it.
	TypeText(text string) error
	PressChord(chord Chord) error

	// Launch asks the OS to start (or foreground, on macOS) the named
	// application: `open -a` on darwin, `cmd /c start` on windows,
	// direct exec on linux. It does not wait for a window to appear —
	// callers compose with the public WaitForWindow poll.
	Launch(app string) error

	// CheckScreenCapture / RequestScreenCapture mirror the
	// CheckAccessibility / RequestAccessibility pair for the macOS
	// Screen Recording permission. Check never prompts; Request may
	// surface the system dialog once. Both are no-ops on linux and
	// windows (true / nil).
	CheckScreenCapture() bool
	RequestScreenCapture() error

	// OverlayScale reports how drawings on the display holding bounds
	// should be scaled: ui multiplies the size metrics (stroke, font) so
	// they look the same physical size everywhere (1 on macOS, whose
	// coordinates are already points; DPI/96 on Windows, whose
	// coordinates are physical pixels), and pixel is how many image
	// pixels make one coordinate unit (the backing scale on macOS, 1 on
	// Windows), so the overlay is crisp on HiDPI screens.
	OverlayScale(bounds Rect) (ui, pixel float64)
	// ShowOverlay puts img (premultiplied RGBA, img size = bounds size
	// x pixel scale) on screen over bounds in a transparent, click-
	// through, never-focused, topmost window, and blocks pumping the
	// platform event loop until done() reports true. Needs no admin
	// rights and no macOS permission. On macOS it must run on the main
	// thread (the darwin adapter locks the main goroutine to it).
	ShowOverlay(bounds Rect, img *image.RGBA, done func() bool) error

	// AccessibleTexts walks the window's accessibility tree (AX on
	// macOS, UI Automation on Windows) and returns every element that
	// has a label, with its global bounds and normalized role. Needs the
	// Accessibility grant on macOS (ErrAccessibilityDenied).
	AccessibleTexts(windowID string) ([]TextItem, error)
	// RecognizeText OCRs what is on screen inside bounds (Vision on
	// macOS, Windows.Media.Ocr on Windows) and returns lines with word
	// boxes, in global coordinates. Needs Screen Recording on macOS
	// (ErrScreenCaptureDenied). The caller makes the window visible.
	RecognizeText(bounds Rect) ([]TextItem, error)
}

// MouseButton identifies which button MouseClick presses.
type MouseButton int

const (
	MouseLeft MouseButton = iota
	MouseRight
	MouseMiddle
)

// Chord is one key pressed with zero or more modifiers held. Key is a
// normalized lower-case name ("a".."z", "0".."9", "f1".."f12",
// "enter", "tab", "esc", "space", "up", "down", "left", "right",
// "delete", "backspace", "home", "end", "pageup", "pagedown", or a
// literal punctuation rune). Cmd means the Command key on macOS and
// the Windows/Super key elsewhere; parsing of user-facing combo
// strings (aliases like "opt", "win") lives in the public package.
type Chord struct {
	Cmd   bool
	Ctrl  bool
	Alt   bool
	Shift bool
	Key   string
}

// TargetedInput is implemented by adapters that can deliver keyboard
// input to one window's process without giving it OS focus (macOS
// CGEventPostToPid, Windows PostMessage to the window's focused child).
// Optional: the engine type-asserts for it and falls back to the
// focus-and-verify path when an adapter lacks it.
type TargetedInput interface {
	TypeTextTo(w Window, text string) error
	PressChordTo(w Window, chord Chord) error
}

// FocusedTextReader reads the value of the target window's focused text
// element (AX on macOS, WM_GETTEXT on Windows) so typed input can be
// verified by state rather than by screenshot. ok is false when the
// window has no readable text element.
type FocusedTextReader interface {
	FocusedText(w Window) (text string, ok bool, err error)
}

// WebReader reads and acts on the web content of a browser window
// (macOS AXWebArea). WebTree walks the selected tab's web area, bounded by
// maxNodes and maxDepth, and returns the window's id (resolved through
// accessibility when w came without one: a window on another Space). WebAct walks again and performs action on the node
// at preorder index, only if that node still has role and label (WebLabel);
// otherwise it does nothing and returns ErrWebStale.
type WebReader interface {
	WebTree(w Window, maxNodes, maxDepth int) ([]WebNode, string, error)
	WebAct(w Window, maxNodes, maxDepth, index, action int, role, label string) error
	// WebPrepare makes the page visible to accessibility; restore undoes
	// whatever it turned on.
	WebPrepare(w Window) (restore func(), err error)
	// WebFocus focuses the node at index (if it still has role and label),
	// verifies it is the app's focused element, and returns a handle that
	// re-checks that same element later. ErrWebStale / ErrWebFocus otherwise.
	WebFocus(w Window, maxNodes, maxDepth, index int, role, label string) (WebTarget, error)
}

// WebTarget is a focused web element held for identity checks.
type WebTarget interface {
	// StillFocused reports whether the app's focused element is still this
	// very element (not just one with the same label).
	StillFocused() (bool, error)
	Release()
}

// ElementPresser activates the accessibility element of window w at the
// global point (x, y) without moving the pointer or focusing the window
// (macOS AXPress, Windows UIA Invoke).
type ElementPresser interface {
	PressElementAt(w Window, x, y int) error
}
