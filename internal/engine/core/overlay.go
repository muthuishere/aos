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

// CoverOverlayer is the optional adapter capability behind `draw cover`:
// a window that blocks a part of the screen and can follow another
// window, i.e. move, resize, hide and show while it is up. Every call
// must come from the goroutine that opened it (the main thread on macOS).
type CoverOverlayer interface {
	// OpenCover shows img over bounds (global units; img is bounds x
	// pixel scale, premultiplied) in a never-focused window above every
	// normal window and absent from the Dock and app switcher. It is
	// click-through unless blockInput, in which case the opaque pixels
	// swallow clicks and scrolls.
	OpenCover(bounds Rect, img *image.RGBA, blockInput bool) (CoverWindow, error)
	// PumpOverlays runs the platform event loop for about d.
	PumpOverlays(d time.Duration)
}

// CoverWindow is one window opened by OpenCover.
type CoverWindow interface {
	// Update moves the window to bounds and shows img (any size: bounds
	// x pixel scale) in one step, so no frame shows the old picture at
	// the new place or the new picture at the old one.
	Update(bounds Rect, img *image.RGBA) error
	// SetVisible hides or shows the window without closing it.
	SetVisible(visible bool)
	Close()
}
