package engine

import (
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Redacted screenshots (#118 P2): covers are painted into the pixels of
// the file itself, so no overlay is needed and a lagging one cannot
// leak. Fail closed: if any cover cannot be placed, is a tint, or does
// not read back as the cover, nothing is written.

// ScreenCover is one area to redact in a screenshot. Exactly one of Rect
// (global units) or Match (the whole window), optionally with Find (the
// text inside Match, or anywhere in the capture without Match: every
// match is covered).
type ScreenCover struct {
	Rect  *Rect      `json:"Rect,omitempty"`
	Match *Match     `json:"Match,omitempty"`
	Find  string     `json:"Find,omitempty"`
	Pad   int        `json:"Pad,omitempty"`
	Style CoverStyle `json:"Style"`
}

// ScreenshotResult is what a screenshot captured and what it covered.
type ScreenshotResult struct {
	Rect    Rect   `json:"Rect"`
	Covered []Rect `json:"Covered,omitempty"`
}

// ScreenshotCovered is Screenshot with opts.Covers painted into the
// file. With no covers it is a plain screenshot.
func ScreenshotCovered(opts ScreenshotOptions) (ScreenshotResult, error) {
	return screenshotCoveredWith(defaultAdapter, opts)
}

type placedCover struct {
	rect Rect
	look coverLook
}

func screenshotCoveredWith(a Adapter, opts ScreenshotOptions) (ScreenshotResult, error) {
	if len(opts.Covers) == 0 {
		r, err := screenshotWith(a, opts)
		return ScreenshotResult{Rect: r}, err
	}
	if opts.OutPath == "" {
		return ScreenshotResult{}, errors.New("screenshot: output path is required")
	}
	rect, err := resolveScreenshotRect(a, opts)
	if err != nil {
		return ScreenshotResult{}, err
	}
	// Place every cover before capturing: a missing one means no capture.
	var placed []placedCover
	for i, c := range opts.Covers {
		ps, err := placeScreenCover(a, c, rect)
		if err != nil {
			return ScreenshotResult{}, fmt.Errorf("screenshot --cover #%d: %w", i+1, err)
		}
		placed = append(placed, ps...)
	}

	dir, err := os.MkdirTemp("", "aos-redact-")
	if err != nil {
		return ScreenshotResult{}, err
	}
	defer os.RemoveAll(dir)
	raw := filepath.Join(dir, "raw.png")
	if err := a.CaptureRect(rect, raw); err != nil {
		return ScreenshotResult{}, err
	}
	img, err := readPNG(raw)
	if err != nil {
		return ScreenshotResult{}, fmt.Errorf("screenshot: read capture: %w", err)
	}
	covered, err := redact(img, rect, placed)
	if err != nil {
		return ScreenshotResult{}, err
	}
	if err := writePNGAtomic(opts.OutPath, img); err != nil {
		return ScreenshotResult{}, err
	}
	return ScreenshotResult{Rect: rect, Covered: covered}, nil
}

// placeScreenCover resolves a cover to the global rects it hides.
func placeScreenCover(a Adapter, c ScreenCover, capture Rect) ([]placedCover, error) {
	l, err := c.Style.look()
	if err != nil {
		return nil, err
	}
	if l.alpha < 1 {
		return nil, ErrCoverTint
	}
	l.radius = 0 // square: a rounded corner would leave pixels showing
	pad := func(r Rect) placedCover {
		return placedCover{Rect{X: r.X - c.Pad, Y: r.Y - c.Pad, W: r.W + 2*c.Pad, H: r.H + 2*c.Pad}, l}
	}
	switch {
	case c.Rect != nil && (c.Match != nil || c.Find != ""):
		return nil, errors.New("give a rect, a window, or text to find, not several")
	case c.Rect != nil:
		if c.Rect.W <= 0 || c.Rect.H <= 0 {
			return nil, errors.New("the cover rect needs a positive width and height")
		}
		return []placedCover{pad(*c.Rect)}, nil
	case c.Find != "":
		var hits []FoundText
		if c.Match != nil {
			// NoRaise: finding must not change what is captured.
			if hits, err = findTextWith(a, FindOptions{Match: *c.Match, Text: c.Find, NoRaise: true}); err != nil {
				return nil, err
			}
		} else {
			lines, err := a.RecognizeText(capture)
			if err != nil {
				return nil, fmt.Errorf("cannot scan the screen for %q: %w", c.Find, err)
			}
			if hits = rankMatches(lines, strings.TrimSpace(c.Find), FindOptions{Text: c.Find}); len(hits) == 0 {
				return nil, notOnScreen(c.Find)
			}
		}
		var out []placedCover
		for _, h := range hits {
			out = append(out, pad(h.Bounds))
		}
		return out, nil
	case c.Match != nil:
		w, err := matchOne(a, *c.Match)
		if err != nil {
			return nil, err
		}
		return []placedCover{pad(w.Bounds)}, nil
	}
	return nil, errors.New("a cover needs a rect, a window or text to find")
}

// redact paints each cover into img (which shows the global rect
// capture) and reads every covered pixel back. Covers are rounded
// outwards to whole pixels.
func redact(img *image.RGBA, capture Rect, covers []placedCover) ([]Rect, error) {
	if capture.W <= 0 || capture.H <= 0 {
		return nil, errors.New("screenshot: empty capture")
	}
	sx := float64(img.Rect.Dx()) / float64(capture.W)
	sy := float64(img.Rect.Dy()) / float64(capture.H)
	var covered []Rect
	for _, c := range covers {
		x0 := int(math.Floor(float64(c.rect.X-capture.X) * sx))
		y0 := int(math.Floor(float64(c.rect.Y-capture.Y) * sy))
		x1 := int(math.Ceil(float64(c.rect.X+c.rect.W-capture.X) * sx))
		y1 := int(math.Ceil(float64(c.rect.Y+c.rect.H-capture.Y) * sy))
		full := image.Rect(x0, y0, x1, y1).Add(img.Rect.Min)
		clip := full.Intersect(img.Rect)
		if clip.Empty() {
			continue // outside this capture: nothing of it is in the file
		}
		box := renderCover(c.look, float64(full.Dx())/sx, float64(full.Dy())/sy, sx, nil)
		if box.Rect.Dx() < full.Dx() || box.Rect.Dy() < full.Dy() {
			// sx != sy: stretch-free fallback, a plain fill covers it all.
			box = renderCover(coverLook{fill: c.look.fill, alpha: 1}, float64(full.Dx()), float64(full.Dy()), 1, nil)
		}
		at := clip.Min.Sub(full.Min)
		draw.Draw(img, clip, box, at, draw.Src)
		// Read back: every pixel is the cover's, fully opaque.
		for y := clip.Min.Y; y < clip.Max.Y; y++ {
			for x := clip.Min.X; x < clip.Max.X; x++ {
				got := img.RGBAAt(x, y)
				want := box.RGBAAt(x-full.Min.X, y-full.Min.Y)
				if got.A != 255 || got != want {
					return nil, fmt.Errorf("screenshot: redaction check failed at %d,%d: nothing written", x, y)
				}
			}
		}
		covered = append(covered, c.rect)
	}
	return covered, nil
}

func readPNG(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, src.Bounds().Dx(), src.Bounds().Dy()))
	draw.Draw(img, img.Rect, src, src.Bounds().Min, draw.Src)
	return img, nil
}

// writePNGAtomic writes img next to path and renames it into place, so
// path is either absent or the finished, redacted image.
func writePNGAtomic(path string, img image.Image) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".aos-shot-*.png")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(name)
		}
	}()
	if err := png.Encode(tmp, img); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// notOnScreen is a --cover-find miss: exit 3, like any not-found.
type notOnScreen string

func (e notOnScreen) Error() string { return fmt.Sprintf("%q is not on screen", string(e)) }
func (e notOnScreen) Unwrap() error { return ErrNoMatch }
