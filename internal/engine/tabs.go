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
