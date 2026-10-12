package core

import (
	"image"
	"time"
)

// OverlayAnimator is the optional adapter capability behind animated
// drawings: overlay windows whose picture and opacity can change while
// they are shown. Adapters without it (linux) fall back to ShowOverlay.
// Every call must come from the one goroutine that opened the windows,
// locked to its OS thread (on macOS, the main thread).
type OverlayAnimator interface {
	// OpenOverlay shows img over bounds (global units; img is bounds x
	// pixel scale, premultiplied) in the same kind of click-through,
	// never-focused, topmost window ShowOverlay uses, at opacity 0..1.
	OpenOverlay(bounds Rect, img *image.RGBA, opacity float64) (Overlay, error)
	// PumpOverlays runs the platform event loop for about d, so window
	// changes reach the screen; it is also the frame pacing sleep.
	PumpOverlays(d time.Duration)
	// ReduceMotion reports the OS "reduce motion" accessibility setting
	// (macOS) or disabled client-area animations (Windows).
	ReduceMotion() bool
}

// Overlay is one window opened by OpenOverlay.
type Overlay interface {
	// SetImage replaces the picture; img has the size given at open.
	SetImage(img *image.RGBA) error
	// SetOpacity changes the whole window's opacity, 0..1.
	SetOpacity(a float64)
	Close()
}
