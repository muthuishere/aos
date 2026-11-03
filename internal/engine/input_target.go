package engine

import (
	"errors"
	"fmt"
	"os"
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
	// NoFallback refuses (ErrNeedsFocus) instead of taking the foreground
	// when there is no focus-free route.
	NoFallback bool

	canSetText bool // the adapter can set a field's text without focus
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

// terminalApps take modifier chords focus-free on Windows: keys go into
// the shell's console input buffer with their modifier state.
var terminalApps = map[string]bool{"windowsterminal": true, "openconsole": true, "conhost": true,
	"cmd": true, "powershell": true, "pwsh": true}

// ErrNeedsFocus is returned (wrapped) when input cannot reach the window
// without taking the foreground and NoFallback was set.

// BeforeFocus runs before aos takes the foreground on its own (the
// fallback when no focus-free route works). The CLI sets it to the polite
// wait: hold off while the person is typing. nil = go ahead.
var ErrNeedsFocus = errors.New("focus-free input is not possible here")

var BeforeFocus func() error

func beforeFocus() error {
	if BeforeFocus == nil {
		return nil
	}
	return BeforeFocus()
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
	if chord == nil && opts.canSetText {
		// Text set through accessibility (Windows UIA / EM_REPLACESEL)
		// reaches UWP, WinUI and Chromium apps without focus.
		return RoutePost, ""
	}
	for _, app := range focusOnlyApps[goos] {
		if strings.EqualFold(w.App, app) {
			return RouteFocus, fmt.Sprintf("%s ignores posted keyboard events", w.App)
		}
	}
	if goos == "windows" && chord != nil && (chord.Cmd || chord.Ctrl || chord.Alt || chord.Shift) && !terminalApps[strings.ToLower(w.App)] {
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
	setter, canSet := a.(core.ElementTextSetter)
	opts.canSetText = canSet
	return deliver(a, goos, match, nil, opts, func(ti core.TargetedInput, w Window) (string, error) {
		if canSet {
			how, err := setter.SetTextTo(w, text)
			if err == nil {
				return how, nil
			}
			if ti == nil {
				return "", err
			}
			if perr := ti.TypeTextTo(w, text); perr != nil {
				return "", fmt.Errorf("%v; posting keys: %v", err, perr)
			}
			return "posted WM_CHAR", nil
		}
		return "", ti.TypeTextTo(w, text)
	}, func() error { return a.TypeText(text) }, text)
}

func pressToWith(a Adapter, goos string, match Match, chord Chord, opts InputOptions) (InputResult, error) {
	return deliver(a, goos, match, &chord, opts, func(ti core.TargetedInput, w Window) (string, error) {
		return "", ti.PressChordTo(w, chord)
	}, func() error { return a.PressChord(chord) }, "")
}

// verifyTimeout bounds how long Verify polls the focused text element.
const verifyTimeout = 2 * time.Second

func deliver(a Adapter, goos string, match Match, chord *Chord, opts InputOptions,
	post func(core.TargetedInput, Window) (string, error), real func() error, text string) (InputResult, error) {
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

	if route == RouteFocus && !opts.Focus && opts.NoFallback {
		return res, fmt.Errorf("%w for %s: %s", ErrNeedsFocus, w.App, reason)
	}
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

	if route == RoutePost {
		guard := guardForeground(a, w)
		how, err := post(ti, w)
		guard()
		switch {
		case err == nil:
			if how != "" {
				res.Reason = how
			}
		case opts.NoFallback:
			return res, fmt.Errorf("%w for %s: %v", ErrNeedsFocus, w.App, err)
		default:
			// No focus-free way in: take focus for the moment it takes,
			// then hand it and the pointer back.
			route, res.Route, res.Reason = RouteFocus, RouteFocus, "focus-free input failed ("+err.Error()+"); used the foreground and handed it back"
		}
	}
	if route == RouteFocus {
		if err := beforeFocus(); err != nil {
			return res, err
		}
		back := takeFocus(a, w.ID)
		if err := ensureFocused(a, Match{App: w.App, Title: w.Title}); err != nil {
			back()
			return res, err
		}
		err := real()
		// Hand focus back to whoever had it — usually the person working
		// beside the agent — so the fallback is a blip, not a takeover.
		back()
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

// guardForeground is for the focus-free routes: some apps activate
// themselves when acted on (a just-launched UWP app on its first Invoke, a
// Win32 edit through its UIA proxy). If the target became the focused
// window during the action, focus goes back to the window that had it.
func guardForeground(a Adapter, target Window) func() {
	prev, ok := focusedWindow(a)
	if !ok || prev.ID == target.ID {
		return func() {}
	}
	mine := func(w Window) bool { return w.ID == target.ID || (target.PID > 0 && w.PID == target.PID) }
	return func() {
		// The activation can land a moment after the call returns (a
		// just-launched UWP app activates on its first Invoke, ~0.5 s).
		checks := guardChecks
		if checks > 1 && strings.EqualFold(target.App, "ApplicationFrameHost") {
			checks = 20 // UWP: measured ~1 s from Invoke to activation
		}
		for i := 0; i < checks; i++ {
			if now, ok := focusedWindow(a); ok && mine(now) {
				// Getting past the foreground lock needs one injected key;
				// only when focus was explicitly allowed (--force sets it).
				if q, ok := a.(interface{ FocusNoInput(string) error }); ok && !strings.EqualFold(os.Getenv("AOS_FOCUS"), "allow") {
					_ = q.FocusNoInput(prev.ID) // no injected input for an automatic hand-back
				} else {
					_ = a.Focus(prev.ID)
				}
				return
			}
			sleepFn(guardSettle)
		}
	}
}

// guardSettle is how long guardForeground waits between its checks, and
// guardChecks how many it makes: only Windows apps were seen activating
// late, so elsewhere it checks once and adds no delay.
var (
	guardSettle = 100 * time.Millisecond
	guardChecks = map[bool]int{true: 8, false: 1}[runtime.GOOS == "windows"]
)

// takeFocus remembers the focused window and the pointer, and returns the
// handback that restores both (the window only when it is not target).
func takeFocus(a Adapter, target string) func() {
	prev, hadPrev := focusedWindow(a)
	px, py, perr := a.CursorPosition()
	return func() {
		if hadPrev && prev.ID != target {
			_ = a.Focus(prev.ID)
		}
		if perr == nil {
			if x, y, err := a.CursorPosition(); err == nil && (x != px || y != py) {
				_ = a.MouseMove(px, py)
			}
		}
	}
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
		guard := guardForeground(a, w)
		err := p.PressElementAt(w, hit.Center.X, hit.Center.Y)
		guard()
		if err == nil {
			res.Route = "press"
			return res, nil
		}
		// No accessibility action: click it with the pointer, then hand
		// focus and the pointer back.
	}
	if err := beforeFocus(); err != nil {
		return res, err
	}
	back := takeFocus(a, w.ID)
	defer back()
	if err := ensureFocused(a, find.Match); err != nil {
		return res, err
	}
	res.Route = "click"
	return res, a.MouseClick(hit.Center.X, hit.Center.Y, MouseLeft, 1)
}
