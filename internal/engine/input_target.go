package engine

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/engine/core"
)

// Focus-free input.
//
// A person may keep working on another monitor while an agent drives aos.
// Anything that needs OS focus is then a race against them: their next click
// moves focus and the agent's keystrokes land in their app. So input aimed at
// a named window goes to that window's process directly (macOS
// CGEventPostToPid, Windows PostMessage to the window's focused control) and
// never through "whatever is frontmost". The old focus-then-type path stays,
// but only when asked for (--focus), or for an app where posting is known not
// to work, and it still refuses to type unless the target holds focus.

// InputOptions steers TypeTo / PressTo.
type InputOptions struct {
	// Focus forces the focus-and-verify path: raise the window, confirm it
	// has focus, then send real keystrokes. The previously focused window
	// is given focus back afterwards.
	Focus bool
	// Verify reads the target's focused text element after typing and
	// fails unless the text arrived (macOS AX value, Windows WM_GETTEXT).
	Verify bool
}

// Input routes.
const (
	RoutePost  = "post"  // delivered to the process / control, no focus change
	RouteFocus = "focus" // focused, verified, then real keystrokes
)

// InputResult says how input was delivered and what was read back.
type InputResult struct {
	Route    string `json:"Route"`
	Reason   string `json:"Reason,omitempty"` // why the focus route was used
	Window   Window `json:"Window"`
	Verified bool   `json:"Verified"`
	Value    string `json:"Value,omitempty"` // the focused element's text after typing (with Verify)
}

// ErrNotVerified is returned (wrapped) when --verify could not confirm the
// text arrived.
var ErrNotVerified = errors.New("input not verified")

// focusOnlyApps are apps that ignore keyboard events posted to their process
// and only take input through the real event stream (macOS Chromium-family
// browsers route keys through their own input pipeline). For them the focus
// route is used automatically.
var focusOnlyApps = map[string][]string{
	"darwin": {
		"Google Chrome", "Google Chrome Canary", "Chromium", "Brave Browser",
		"Microsoft Edge", "Arc", "Vivaldi", "Opera",
	},
	// UWP / WinUI apps live in ApplicationFrameHost and have no Win32
	// control to post to.
	"windows": {"ApplicationFrameHost", "CalculatorApp"},
}

// chooseRoute decides how input reaches w. It is pure so the routing rules
// are unit-tested without a screen. chord is nil for typed text.
func chooseRoute(goos string, hasTargeted bool, w Window, chord *Chord, opts InputOptions) (route, reason string) {
	if opts.Focus {
		return RouteFocus, "--focus requested"
	}
	if !hasTargeted {
		return RouteFocus, "this platform has no focus-free input"
	}
	for _, app := range focusOnlyApps[goos] {
		if strings.EqualFold(w.App, app) {
			return RouteFocus, fmt.Sprintf("%s ignores posted keyboard events", w.App)
		}
	}
	if goos == "windows" && chord != nil && (chord.Cmd || chord.Ctrl || chord.Alt || chord.Shift) {
		// Posted messages carry no modifier state: the target reads Ctrl/Alt
		// from the real keyboard, so a posted ctrl+s arrives as plain s.
		return RouteFocus, "modifier chords need real keyboard state on Windows"
	}
	return RoutePost, ""
}

// TypeTo types text into the window matched by match without needing it to
// have focus (see InputOptions for the fallback and verification rules).
func TypeTo(match Match, text string, opts InputOptions) (InputResult, error) {
	return typeToWith(defaultAdapter, runtime.GOOS, match, text, opts)
}

// PressTo presses combo in the window matched by match without needing it to
// have focus.
func PressTo(match Match, combo string, opts InputOptions) (InputResult, error) {
	chord, err := ParseChord(combo)
	if err != nil {
		return InputResult{}, err
	}
	return pressToWith(defaultAdapter, runtime.GOOS, match, chord, opts)
}

func typeToWith(a Adapter, goos string, match Match, text string, opts InputOptions) (InputResult, error) {
	if text == "" {
		return InputResult{}, errors.New("type: text cannot be empty")
	}
	return deliver(a, goos, match, nil, opts, func(ti core.TargetedInput, w Window) error {
		return ti.TypeTextTo(w, text)
	}, func() error { return a.TypeText(text) }, text)
}

func pressToWith(a Adapter, goos string, match Match, chord Chord, opts InputOptions) (InputResult, error) {
	return deliver(a, goos, match, &chord, opts, func(ti core.TargetedInput, w Window) error {
		return ti.PressChordTo(w, chord)
	}, func() error { return a.PressChord(chord) }, "")
}

// verifyTimeout bounds how long Verify polls the focused text element.
const verifyTimeout = 2 * time.Second

func deliver(a Adapter, goos string, match Match, chord *Chord, opts InputOptions,
	post func(core.TargetedInput, Window) error, real func() error, text string) (InputResult, error) {
	if match.App == "" && match.Title == "" {
		return InputResult{}, errors.New("input: name the target window with --app or --title")
	}
	w, err := matchStamped(a, match)
	if err != nil {
		return InputResult{}, fmt.Errorf("%w: %s", err, describe(match))
	}
	ti, hasTargeted := a.(core.TargetedInput)
	route, reason := chooseRoute(goos, hasTargeted, w, chord, opts)
	res := InputResult{Route: route, Reason: reason, Window: w}

	reader, canRead := a.(core.FocusedTextReader)
	verify := opts.Verify && text != ""
	var before string
	if verify {
		if !canRead {
			return res, fmt.Errorf("%w: --verify cannot read text fields on this platform", ErrNotVerified)
		}
		b, ok, err := reader.FocusedText(w)
		if err != nil {
			return res, fmt.Errorf("%w: reading %s before typing: %v", ErrNotVerified, w.App, err)
		}
		if !ok {
			return res, fmt.Errorf("%w: %s has no readable focused text element, so the text cannot be checked — input NOT sent", ErrNotVerified, w.App)
		}
		before = b
	}

	switch route {
	case RoutePost:
		if err := post(ti, w); err != nil {
			return res, fmt.Errorf("focus-free input to %s failed: %w (retry with --focus to type through real focus)", w.App, err)
		}
	default:
		prev, hadPrev := focusedWindow(a)
		if err := ensureFocused(a, Match{App: w.App, Title: w.Title}); err != nil {
			return res, err
		}
		err := real()
		// Hand focus back to whoever had it — usually the person working
		// beside the agent — so the fallback is a blip, not a takeover.
		if hadPrev && prev.ID != w.ID {
			_ = a.Focus(prev.ID)
		}
		if err != nil {
			return res, err
		}
	}

	if verify {
		deadline := nowFn().Add(verifyTimeout)
		for {
			after, ok, err := reader.FocusedText(w)
			if err == nil && ok && textArrived(before, after, text) {
				res.Verified, res.Value = true, after
				return res, nil
			}
			if !nowFn().Before(deadline) {
				hint := ""
				if route == RoutePost {
					hint = " — this app may ignore posted events; retry with --focus"
				}
				return res, fmt.Errorf("%w: %q did not appear in %s's focused text element within %s%s", ErrNotVerified, text, w.App, verifyTimeout, hint)
			}
			sleepFn(100 * time.Millisecond)
		}
	}
	return res, nil
}

// textArrived reports whether after holds one more copy of text than before.
// Line endings are normalised: text fields store \r or \n for a typed newline.
func textArrived(before, after, text string) bool {
	// Case-insensitive: apps auto-capitalise ("hello" arrives as "Hello").
	n := func(s string) string {
		return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
	}
	return strings.Count(n(after), n(text)) > strings.Count(n(before), n(text))
}

// matchStamped is matchOne with the Monitor / Focused stamps filled in.
func matchStamped(a Adapter, match Match) (Window, error) {
	ws, err := listWindowsWith(a, Filter{Title: match.Title, App: match.App})
	if err != nil {
		return Window{}, err
	}
	if len(ws) == 0 {
		return Window{}, ErrNoMatch
	}
	return pickInputWindow(ws), nil
}

// pickInputWindow prefers a real document window over the untitled strips,
// popups and tooltips an app also owns (TextEdit lists a 68x22 helper
// before its document): titled and window-sized first, then window-sized,
// then whatever came first.
func pickInputWindow(ws []Window) Window {
	big := func(w Window) bool { return w.Bounds.W >= 120 && w.Bounds.H >= 80 }
	for _, w := range ws {
		if w.Title != "" && big(w) {
			return w
		}
	}
	for _, w := range ws {
		if big(w) {
			return w
		}
	}
	return ws[0]
}

func focusedWindow(a Adapter) (Window, bool) {
	ws, err := listWindowsWith(a, Filter{})
	if err != nil {
		return Window{}, false
	}
	for _, w := range ws {
		if w.Focused {
			return w, true
		}
	}
	return Window{}, false
}

func describe(m Match) string {
	var parts []string
	if m.App != "" {
		parts = append(parts, "app "+m.App)
	}
	if m.Title != "" {
		parts = append(parts, "title "+m.Title)
	}
	return strings.Join(parts, " and ")
}

// ClickResult reports a click on a found element.
type ClickResult struct {
	Route  string    `json:"Route"` // "press" (accessibility action) or "click" (pointer)
	Window Window    `json:"Window"`
	Found  FoundText `json:"Found"`
}

// ClickFound finds text in the matched window and activates it. It prefers
// the accessibility action (AXPress / UIA Invoke), which moves no pointer
// and needs no focus. Only with focus=true does it fall back to raising the
// window and clicking the element's centre with the real pointer.
func ClickFound(find FindOptions, focus bool) (ClickResult, error) {
	return clickFoundWith(defaultAdapter, find, focus)
}

func clickFoundWith(a Adapter, find FindOptions, focus bool) (ClickResult, error) {
	if !focus {
		// OCR raises the window; stay in the accessibility tree.
		if find.Source == "" || find.Source == FindAuto {
			find.Source = FindAccessibility
		}
		find.NoRaise = true
	}
	hits, err := findTextWith(a, find)
	if err != nil {
		return ClickResult{}, err
	}
	w, err := matchStamped(a, find.Match)
	if err != nil {
		return ClickResult{}, err
	}
	hit := hits[0]
	res := ClickResult{Window: w, Found: hit}
	if p, ok := a.(core.ElementPresser); ok && !focus {
		if err := p.PressElementAt(w, hit.Center.X, hit.Center.Y); err == nil {
			res.Route = "press"
			return res, nil
		} else {
			return res, fmt.Errorf("could not press %q in %s through accessibility: %v (retry with --focus to click it with the pointer)", hit.Label, w.App, err)
		}
	}
	if !focus {
		return res, errors.New("this platform has no accessibility press; retry with --focus to click with the pointer")
	}
	if err := ensureFocused(a, find.Match); err != nil {
		return res, err
	}
	res.Route = "click"
	return res, a.MouseClick(hit.Center.X, hit.Center.Y, MouseLeft, 1)
}
