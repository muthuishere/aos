package engine

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// MouseMove warps the cursor to (x, y). Coordinates are
// monitor-relative when monitorID is non-nil, absolute virtual-desktop
// points otherwise — the same rule MoveCoords uses.
func MouseMove(monitorID *int, x, y int) error {
	return mouseMoveWith(defaultAdapter, monitorID, x, y)
}

func mouseMoveWith(a Adapter, monitorID *int, x, y int) error {
	gx, gy, err := resolvePoint(a, monitorID, x, y)
	if err != nil {
		return err
	}
	return a.MouseMove(gx, gy)
}

// ClickOptions parameterizes MouseClick. X/Y nil means "click where
// the cursor already is" (both must be nil or both set). When set,
// they follow the monitor-relative rule: relative when Monitor is
// non-nil, absolute otherwise. Double sends a double-click.
type ClickOptions struct {
	Monitor *int
	X, Y    *int
	Button  MouseButton
	Double  bool
}

// MouseClick presses and releases a mouse button, optionally moving to
// a target point first.
func MouseClick(opts ClickOptions) error {
	return mouseClickWith(defaultAdapter, opts)
}

func mouseClickWith(a Adapter, opts ClickOptions) error {
	if (opts.X == nil) != (opts.Y == nil) {
		return errors.New("click: x and y must be given together")
	}
	clicks := 1
	if opts.Double {
		clicks = 2
	}
	var gx, gy int
	if opts.X == nil {
		var err error
		gx, gy, err = a.CursorPosition()
		if err != nil {
			return err
		}
	} else {
		var err error
		gx, gy, err = resolvePoint(a, opts.Monitor, *opts.X, *opts.Y)
		if err != nil {
			return err
		}
	}
	return a.MouseClick(gx, gy, opts.Button, clicks)
}

// CursorPosition reports the cursor's current virtual-desktop point.
func CursorPosition() (int, int, error) {
	return defaultAdapter.CursorPosition()
}

// resolvePoint applies the shared monitor-relative rule to a single
// point: offset by the monitor's origin when monitorID is set, pass
// through untouched otherwise.
func resolvePoint(a Adapter, monitorID *int, x, y int) (int, int, error) {
	if monitorID == nil {
		return x, y, nil
	}
	monitors, err := listMonitorsWith(a)
	if err != nil {
		return 0, 0, err
	}
	mon, err := findMonitor(monitors, *monitorID)
	if err != nil {
		return 0, 0, err
	}
	return mon.X + x, mon.Y + y, nil
}

// TypeText injects text as literal unicode into the focused window —
// whatever that happens to be. Prefer TypeInto when the target window
// is known: keystrokes delivered while focus is mid-flight land in the
// previously focused window.
func TypeText(text string) error {
	if text == "" {
		return errors.New("type: text cannot be empty")
	}
	return defaultAdapter.TypeText(text)
}

// TypeInto focuses the matched window, verifies the focus actually
// landed (poll until the matched window reports Focused), and only
// then types. This is the safe path for scripted input: it turns
// "typed into the wrong window" from a silent data-corruption hazard
// into a hard error.
func TypeInto(match Match, text string) error {
	return typeIntoWith(defaultAdapter, match, text)
}

func typeIntoWith(a Adapter, match Match, text string) error {
	if text == "" {
		return errors.New("type: text cannot be empty")
	}
	if err := ensureFocused(a, match); err != nil {
		return err
	}
	return a.TypeText(text)
}

// PressKeyInto is PressKey with the same focus-and-verify guard as
// TypeInto.
func PressKeyInto(match Match, combo string) error {
	return pressKeyIntoWith(defaultAdapter, match, combo)
}

func pressKeyIntoWith(a Adapter, match Match, combo string) error {
	chord, err := ParseChord(combo)
	if err != nil {
		return err
	}
	if err := ensureFocused(a, match); err != nil {
		return err
	}
	return a.PressChord(chord)
}

// focusVerifyTimeout bounds how long ensureFocused waits for the
// window system to report the matched window as focused.
const focusVerifyTimeout = 2 * time.Second

// focusSettleDelay is how long to let a freshly focused application ready its
// input before keystrokes are sent to it. Short enough to be invisible in a
// script, long enough to cover an app still painting its first frame.
const focusSettleDelay = 150 * time.Millisecond

// ensureFocused raises the matched window and blocks until the window
// list reports it Focused. On platforms whose adapter cannot say which
// monitor holds focus (no Monitor.Focused flag — linux today) the
// verification is skipped: Focus succeeding is the best signal
// available there.
func ensureFocused(a Adapter, match Match) error {
	if err := focusWith(a, match); err != nil {
		return err
	}
	monitors, err := listMonitorsWith(a)
	if err != nil {
		return err
	}
	verifiable := false
	for _, m := range monitors {
		if m.Focused {
			verifiable = true
			break
		}
	}
	if !verifiable {
		return nil
	}
	deadline := nowFn().Add(focusVerifyTimeout)
	for {
		ws, err := listWindowsWith(a, Filter{Title: match.Title, App: match.App})
		if err != nil {
			return err
		}
		for _, w := range ws {
			if w.Focused {
				// A window reports focused as soon as the OS gives it the
				// keyboard, which is before the application has a caret ready
				// to receive characters. Typing into that gap is silently
				// lossy: Windows 11 Notepad, typed into the instant it was
				// focused, turned "hello from agentic-os" into
				// "hello sssssssssssssss" while the very next line typed
				// cleanly. Let the app settle before the first keystroke.
				sleepFn(focusSettleDelay)
				return nil
			}
		}
		if !nowFn().Before(deadline) {
			return fmt.Errorf("focus verification timed out: matched window did not become focused within %s — input NOT sent (guard against typing into the wrong window)", focusVerifyTimeout)
		}
		sleepFn(waitPollInterval / 5)
	}
}

// PressKey parses a combo string like "cmd+shift+s" or "enter" and
// presses it. Modifier aliases: cmd/command/meta/super/win → Cmd,
// ctrl/control → Ctrl, alt/opt/option → Alt, shift → Shift. The final
// token is the key: a single character, f1..f12, or a named key from
// namedKeys. Letters are lowercased — write shift explicitly
// ("cmd+shift+s", not "cmd+S").
func PressKey(combo string) error {
	chord, err := ParseChord(combo)
	if err != nil {
		return err
	}
	return defaultAdapter.PressChord(chord)
}

// namedKeys is the closed set of multi-character key names ParseChord
// accepts (plus f1..f12, validated separately). Adapters map these to
// platform keycodes; adding a name here requires adding it to all
// three adapters.
var namedKeys = map[string]bool{
	"enter": true, "tab": true, "esc": true, "space": true,
	"up": true, "down": true, "left": true, "right": true,
	"delete": true, "backspace": true,
	"home": true, "end": true, "pageup": true, "pagedown": true,
}

// ParseChord turns a user-facing "+"-separated combo into a core.Chord.
// Exported so library callers can validate combos without pressing them.
func ParseChord(combo string) (Chord, error) {
	trimmed := strings.TrimSpace(combo)
	if trimmed == "" {
		return Chord{}, errors.New("key: combo cannot be empty")
	}
	var chord Chord
	tokens := strings.Split(strings.ToLower(trimmed), "+")
	// "cmd++" (literal plus key) splits into a trailing pair of empty
	// tokens; collapse them back into a "+" key token.
	cleaned := make([]string, 0, len(tokens))
	for i, t := range tokens {
		if t == "" {
			if i == len(tokens)-1 && strings.HasSuffix(trimmed, "+") {
				cleaned = append(cleaned, "+")
			}
			continue
		}
		cleaned = append(cleaned, t)
	}
	if len(cleaned) == 0 {
		return Chord{}, fmt.Errorf("key: cannot parse combo %q", combo)
	}
	for _, mod := range cleaned[:len(cleaned)-1] {
		switch mod {
		case "cmd", "command", "meta", "super", "win":
			chord.Cmd = true
		case "ctrl", "control":
			chord.Ctrl = true
		case "alt", "opt", "option":
			chord.Alt = true
		case "shift":
			chord.Shift = true
		default:
			return Chord{}, fmt.Errorf("key: unknown modifier %q in combo %q", mod, combo)
		}
	}
	key := normalizeKeyName(cleaned[len(cleaned)-1])
	if !validKeyName(key) {
		return Chord{}, fmt.Errorf("key: unknown key %q in combo %q", key, combo)
	}
	chord.Key = key
	return chord, nil
}

func normalizeKeyName(key string) string {
	switch key {
	case "return":
		return "enter"
	case "escape":
		return "esc"
	case "del":
		return "delete"
	case "pgup":
		return "pageup"
	case "pgdn", "pgdown":
		return "pagedown"
	}
	return key
}

func validKeyName(key string) bool {
	if len([]rune(key)) == 1 {
		return true
	}
	if namedKeys[key] {
		return true
	}
	if strings.HasPrefix(key, "f") && len(key) <= 3 {
		n := 0
		if _, err := fmt.Sscanf(key, "f%d", &n); err == nil && n >= 1 && n <= 12 {
			return true
		}
	}
	return false
}

// Launch asks the OS to start (or foreground) the named application.
// It returns as soon as the launcher hands off — compose with
// WaitForWindow to block until the app's window exists.
func Launch(app string) error {
	if strings.TrimSpace(app) == "" {
		return errors.New("launch: app name cannot be empty")
	}
	return defaultAdapter.Launch(app)
}
