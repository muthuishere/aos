package engine

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"
	"math"
	"strings"

	_ "image/gif"  // decoders for image.Decode
	_ "image/jpeg" // decoders for image.Decode
	_ "image/png"  // decoders for image.Decode

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decoder for image.Decode
)

// DrawImage is the picture style: a PNG, JPEG, GIF, WebP or SVG shown on
// screen, click-through, like a label.
const DrawImage DrawStyle = "image"

// maxImageBytes bounds what draw image reads, so a wrong path cannot hang.
const maxImageBytes = 64 << 20

// ReadDrawImage reads at most maxImageBytes from r.
func ReadDrawImage(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("draw: image is larger than %d MB", maxImageBytes>>20)
	}
	if len(data) == 0 {
		return nil, errors.New("draw: the image is empty")
	}
	return data, nil
}

// isSVG sniffs SVG from content (not the file name): after a BOM and
// whitespace, an XML or <svg start.
func isSVG(data []byte) bool {
	head := bytes.TrimLeft(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), " \t\r\n")
	if len(head) > 2048 {
		head = head[:2048]
	}
	return bytes.HasPrefix(head, []byte("<")) && bytes.Contains(bytes.ToLower(head), []byte("<svg"))
}

// svgUnsupported are the SVG features the pure-Go rasterizer silently drops.
// A drawing that quietly misses its text or clipping is worse than an error.
var svgUnsupportedElements = map[string]string{
	"text": "text", "tspan": "text", "textpath": "text",
	"image": "an embedded <image>", "filter": "filters", "mask": "masks",
	"clippath": "clip paths", "pattern": "patterns", "foreignobject": "<foreignObject>",
	"marker": "markers", "switch": "<switch>",
}

// checkSVG refuses an SVG that uses something the renderer cannot draw,
// naming the element, rather than returning a half-drawn picture.
func checkSVG(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil // a parse error is reported by the rasterizer
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		name := strings.ToLower(se.Name.Local)
		if what, bad := svgUnsupportedElements[name]; bad {
			hint := "draw it as paths or shapes"
			if what == "text" {
				hint = "convert the text to outlines (paths), or put the words in a `aos draw bubble` or `aos draw text` label"
			}
			return fmt.Errorf("draw: this SVG uses %s (<%s>), which aos cannot render and would silently leave out; %s", what, se.Name.Local, hint)
		}
		if name == "rect" {
			for _, a := range se.Attr {
				if (a.Name.Local == "rx" || a.Name.Local == "ry") && a.Value != "" && a.Value != "0" {
					return errors.New("draw: this SVG has a <rect> with rounded corners (rx/ry), which aos cannot render and would draw square; use a <path> for the rounded shape")
				}
			}
		}
	}
}

// decodeDrawImage decodes data (sniffed from content) into a size the
// drawing needs: wPx x hPx pixels, premultiplied. wPx or hPx 0 means "keep
// the aspect from the other"; both 0 is the natural size.
func decodeDrawImage(data []byte, wPx, hPx int) (*image.RGBA, error) {
	if isSVG(data) {
		return rasterizeSVG(data, wPx, hPx)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("draw: not an image aos can read (PNG, JPEG, GIF, WebP or SVG): %v", err)
	}
	return scaleImage(src, wPx, hPx), nil
}

// imageNaturalSize is the image's own size in pixels, aspect included.
func imageNaturalSize(data []byte) (int, int, error) {
	if isSVG(data) {
		icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.WarnErrorMode)
		if err != nil {
			return 0, 0, fmt.Errorf("draw: cannot read this SVG: %w", err)
		}
		w, h := icon.ViewBox.W, icon.ViewBox.H
		if w <= 0 || h <= 0 {
			return 0, 0, errors.New("draw: this SVG has no size: give it a viewBox or width and height")
		}
		return int(math.Ceil(w)), int(math.Ceil(h)), nil
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, fmt.Errorf("draw: not an image aos can read (PNG, JPEG, GIF, WebP or SVG): %v", err)
	}
	return cfg.Width, cfg.Height, nil
}

func rasterizeSVG(data []byte, wPx, hPx int) (*image.RGBA, error) {
	if err := checkSVG(data); err != nil {
		return nil, err
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.WarnErrorMode)
	if err != nil {
		return nil, fmt.Errorf("draw: cannot read this SVG: %w", err)
	}
	if wPx <= 0 || hPx <= 0 {
		return nil, errors.New("draw: internal: SVG needs a target size")
	}
	icon.SetTarget(0, 0, float64(wPx), float64(hPx))
	img := image.NewRGBA(image.Rect(0, 0, wPx, hPx))
	icon.Draw(rasterx.NewDasher(wPx, hPx, rasterx.NewScannerGV(wPx, hPx, img, img.Bounds())), 1)
	return img, nil
}

// scaleImage resamples src to wPx x hPx (0 = from the other side, or natural)
// into a premultiplied RGBA.
func scaleImage(src image.Image, wPx, hPx int) *image.RGBA {
	b := src.Bounds()
	if wPx <= 0 && hPx <= 0 {
		wPx, hPx = b.Dx(), b.Dy()
	} else if wPx <= 0 {
		wPx = int(math.Round(float64(hPx) * float64(b.Dx()) / float64(b.Dy())))
	} else if hPx <= 0 {
		hPx = int(math.Round(float64(wPx) * float64(b.Dy()) / float64(b.Dx())))
	}
	wPx, hPx = max(wPx, 1), max(hPx, 1)
	out := image.NewRGBA(image.Rect(0, 0, wPx, hPx))
	if b.Dx() == wPx && b.Dy() == hPx {
		draw.Draw(out, out.Bounds(), src, b.Min, draw.Src) // converts to premultiplied
		return out
	}
	xdraw.CatmullRom.Scale(out, out.Bounds(), src, b, xdraw.Over, nil)
	return out
}

// planImageScene places a picture on its monitor, sized in units: --width /
// --height (px or N% of the container), else its natural size (one pixel is
// 1/PixelScale units, so an @2x image is not doubled) shrunk to fit.
func planImageScene(a Adapter, opts DrawOptions) (*drawScene, error) {
	if len(opts.Image) == 0 {
		return nil, errors.New("draw: image: no image given")
	}
	if opts.Position == "" {
		opts.Position = "center"
	}
	if !opts.Parts.empty() || (isSVG(opts.Image) && svgOpac0Re.Match(opts.Image)) {
		if !isSVG(opts.Image) {
			return nil, errors.New("draw: --show, --hide and --recolor work on an SVG only")
		}
		data, err := ApplySVGParts(opts.Image, opts.Parts)
		if err != nil {
			return nil, err
		}
		opts.Image = data
	}
	pl, err := planPlace(a, opts, "image")
	if err != nil {
		return nil, err
	}
	mx, my, err := pl.margins(opts)
	if err != nil {
		return nil, err
	}
	nw, nh, err := imageNaturalSize(opts.Image)
	if err != nil {
		return nil, err
	}
	if isSVG(opts.Image) {
		if err := checkSVG(opts.Image); err != nil {
			return nil, err
		}
	}
	w, h := float64(nw)/pl.px, float64(nh)/pl.px
	length := func(raw, name string, ref float64) (float64, error) {
		v, err := parseLength(raw, ref)
		if err != nil {
			return 0, fmt.Errorf("draw: --%s %w", name, err)
		}
		if !strings.HasSuffix(strings.TrimSpace(raw), "%") {
			v *= pl.ui
		}
		return v, nil
	}
	switch {
	case opts.Width != "" && opts.Height != "":
		if w, err = length(opts.Width, "width", pl.ref.W); err != nil {
			return nil, err
		}
		if h, err = length(opts.Height, "height", pl.ref.H); err != nil {
			return nil, err
		}
	case opts.Width != "":
		nwUnits, err := length(opts.Width, "width", pl.ref.W)
		if err != nil {
			return nil, err
		}
		h, w = h*nwUnits/w, nwUnits
	case opts.Height != "":
		nhUnits, err := length(opts.Height, "height", pl.ref.H)
		if err != nil {
			return nil, err
		}
		w, h = w*nhUnits/h, nhUnits
	default:
		// natural size, but never wider than the container allows
		room := frect{W: pl.ref.W - 2*mx, H: pl.ref.H - 2*my}
		if f := math.Min(1, math.Min(room.W/w, room.H/h)); f < 1 && f > 0 {
			w, h = w*f, h*f
		}
	}
	w, h = math.Max(1, math.Round(w)), math.Max(1, math.Round(h))
	if w > pl.bounds.W*4 || h > pl.bounds.H*4 {
		return nil, fmt.Errorf("draw: the image would be %.0f x %.0f, far larger than the screen; give --width or --height", w, h)
	}
	pic, err := decodeDrawImage(opts.Image, int(math.Round(w*pl.px)), int(math.Round(h*pl.px)))
	if err != nil {
		return nil, err
	}
	if opts.Opacity > 0 && opts.Opacity < 1 {
		fadePicture(pic, opts.Opacity)
	}
	box := pl.placeBox(opts.Position, w, h, mx, my, opts.Offset)
	anchor := box.center()
	if pl.isPoint {
		anchor = vec{pl.c.X, pl.c.Y}
	}
	res := DrawResult{
		Monitor:    pl.mon.ID,
		Sign:       Rect{X: pl.mon.X + int(box.X), Y: pl.mon.Y + int(box.Y), W: int(box.W), H: int(box.H)},
		Tip:        Point{pl.mon.X + int(math.Round(anchor.X)), pl.mon.Y + int(math.Round(anchor.Y))},
		From:       string(opts.Position),
		Surface:    pl.surface,
		PixelScale: pl.px,
	}
	ts := &textScene{box: box, pic: pic, ui: pl.ui}
	return &drawScene{
		opts: opts, text: ts, px: pl.px, res: res,
		lay:     drawLayout{sign: box, uiScale: pl.ui, anchor: anchor},
		surface: image.Rect(0, 0, int(math.Ceil(pl.bounds.W*pl.px)), int(math.Ceil(pl.bounds.H*pl.px))),
	}, nil
}

// fadePicture scales a premultiplied picture's colour and alpha by k.
func fadePicture(img *image.RGBA, k float64) {
	for i := range img.Pix {
		img.Pix[i] = uint8(float64(img.Pix[i])*k + 0.5)
	}
}

// paintPicture draws pic with its top-left at (x, y) surface units.
func (cv *canvas) paintPicture(pic *image.RGBA, x, y float64) {
	at := image.Pt(int(math.Round(x*cv.px)), int(math.Round(y*cv.px)))
	dst := image.Rectangle{Min: at, Max: at.Add(pic.Bounds().Size())}
	clip := dst.Intersect(cv.img.Rect)
	if clip.Empty() {
		return
	}
	draw.Draw(cv.img, clip, pic, pic.Bounds().Min.Add(clip.Min.Sub(at)), draw.Over)
}
