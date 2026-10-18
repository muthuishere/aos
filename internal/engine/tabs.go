package engine

import (
	"errors"

	"github.com/muthuishere/aos/internal/engine/core"
)

// The tab group reads tab strips out of the accessibility tree when an app
// has no scripting interface. These are thin, ID-based doors onto the
// adapter for it; everything else in this package matches windows by
// app/title, which a tab switch changes under your feet.

// WindowTexts returns every named accessibility element of one window
// (AX on macOS, UI Automation on Windows), with normalized roles.
func WindowTexts(windowID string) ([]TextItem, error) {
	return defaultAdapter.AccessibleTexts(windowID)
}

// FocusWindowID raises one window by its ID.
func FocusWindowID(windowID string) error { return defaultAdapter.Focus(windowID) }

// PressElement activates the element at (x, y) inside w through
// accessibility (AXPress / UIA Invoke or Select), without moving the pointer.
func PressElement(w Window, x, y int) error {
	p, ok := defaultAdapter.(core.ElementPresser)
	if !ok {
		return errors.New("this platform has no accessibility press")
	}
	return p.PressElementAt(w, x, y)
}

// ClickAt clicks the left button once at a global point.
func ClickAt(x, y int) error { return defaultAdapter.MouseClick(x, y, MouseLeft, 1) }

// WebNode is one element of a browser tab's web content.
type WebNode = core.WebNode

// ErrWebStale: the element at a ref is not the one that was read.
var ErrWebStale = core.ErrWebStale

// Web actions for WebAct.
const (
	WebPress = core.WebPress
	WebFocus = core.WebFocus
)

func webReader() (core.WebReader, error) {
	r, ok := defaultAdapter.(core.WebReader)
	if !ok {
		return nil, errors.New("reading web content is not supported on this platform yet")
	}
	return r, nil
}

// WebTree reads the web content of window w's selected tab. When w has no
// ID (it is on another Space, so not in the on-screen list), the ID found
// through accessibility is filled in.
func WebTree(w *Window, maxNodes, maxDepth int) ([]WebNode, error) {
	r, err := webReader()
	if err != nil {
		return nil, err
	}
	nodes, id, err := r.WebTree(*w, maxNodes, maxDepth)
	if w.ID == "" {
		w.ID = id
	}
	return nodes, err
}

// WebAct presses or focuses the web element at preorder index, only when
// it still has role and label; ErrWebStale otherwise.
func WebAct(w Window, maxNodes, maxDepth, index, action int, role, label string) error {
	r, err := webReader()
	if err != nil {
		return err
	}
	return r.WebAct(w, maxNodes, maxDepth, index, action, role, label)
}

func targeted() (core.TargetedInput, error) {
	t, ok := defaultAdapter.(core.TargetedInput)
	if !ok {
		return nil, errors.New("this platform cannot send keys to a background window")
	}
	return t, nil
}

// PostTextTo types text into window w's process without focusing or
// raising it (macOS CGEventPostToPid, Windows PostMessage). Unlike TypeTo it
// never falls back to the focus route, which is what a browser page needs
// once its field has accessibility focus.
func PostTextTo(w Window, text string) error {
	t, err := targeted()
	if err != nil {
		return err
	}
	return t.TypeTextTo(w, text)
}

// PostKeyTo presses combo in window w's process without focusing it.
func PostKeyTo(w Window, combo string) error {
	chord, err := ParseChord(combo)
	if err != nil {
		return err
	}
	t, err := targeted()
	if err != nil {
		return err
	}
	return t.PressChordTo(w, chord)
}
