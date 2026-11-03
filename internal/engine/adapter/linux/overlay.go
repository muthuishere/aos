//go:build linux

package linux

import (
	"image"

	"github.com/muthuishere/aos/internal/engine/core"
)

// OverlayScale: X11 coordinates are pixels; no HiDPI handling yet.
func (a *Adapter) OverlayScale(core.Rect) (float64, float64) { return 1, 1 }

// ShowOverlay is not implemented on linux yet (needs an ARGB X11 window
// with an empty input shape). RenderDraw still works.
func (a *Adapter) ShowOverlay(core.Rect, *image.RGBA, func() bool) error {
	return core.ErrNotImplemented
}
