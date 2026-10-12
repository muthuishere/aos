package engine

// Rasterizer for `windowctl draw`: turns a drawLayout into one
// transparent RGBA image the size of the surface. Pure Go, so the pixels
// are identical on every OS and testable anywhere; the adapters only put
// the finished image on screen.
//
// Layer order follows bigarrow: the target mark, every thin black edge,
// the sign's edge, every contrast border, the sign body, then the
// coloured shaft, root and head, then the sign text. Every border sits
// below every coloured fill, so shaft and head share one continuous
// border and the shaft grows out of the sign with no line across it.

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

const (
	signPadX     = 30.0
	signPadY     = 16.0
	signOutline  = 3.5
	signEdgeLine = 1.5
)

// canvas paints in layout units, scaled by px onto an RGBA image whose
// origin is the surface's top-left.
type canvas struct {
	img *image.RGBA
	px  float64
}

func newCanvas(wPx, hPx int, px float64) *canvas {
	return newCanvasIn(image.Rect(0, 0, wPx, hPx), px)
}

// newCanvasIn paints only the pixels inside r (surface pixel
// coordinates), for animation frames cropped to the drawing.
func newCanvasIn(r image.Rectangle, px float64) *canvas {
	return &canvas{img: image.NewRGBA(r), px: px}
}

func rgba(c Color) color.RGBA {
	return color.RGBA{uint8(c.R*255 + 0.5), uint8(c.G*255 + 0.5), uint8(c.B*255 + 0.5), 255}
}

func (cv *canvas) toPx(pts []vec) []vec {
	out := make([]vec, len(pts))
	for i, p := range pts {
		out[i] = p.mul(cv.px)
	}
	return out
}

func boundsOf(pts []vec, pad float64, clip image.Rectangle) image.Rectangle {
	x0, y0, x1, y1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range pts {
		x0, y0, x1, y1 = math.Min(x0, p.X), math.Min(y0, p.Y), math.Max(x1, p.X), math.Max(y1, p.Y)
	}
	r := image.Rect(int(math.Floor(x0-pad)), int(math.Floor(y0-pad)), int(math.Ceil(x1+pad))+1, int(math.Ceil(y1+pad))+1)
	return r.Intersect(clip)
}

func (cv *canvas) paint(mask *image.Alpha, c Color) {
	draw.DrawMask(cv.img, mask.Rect, image.NewUniform(rgba(c)), image.Point{}, mask, mask.Rect.Min, draw.Over)
}

// fill paints a closed polygon (non-zero, anti-aliased).
func (cv *canvas) fill(pts []vec, c Color) {
	if len(pts) < 3 {
		return
	}
	p := cv.toPx(pts)
	r := boundsOf(p, 1, cv.img.Rect)
	if r.Empty() {
		return
	}
	z := vector.NewRasterizer(r.Dx(), r.Dy())
	o := vec{float64(r.Min.X), float64(r.Min.Y)}
	z.MoveTo(float32(p[0].X-o.X), float32(p[0].Y-o.Y))
	for _, q := range p[1:] {
		z.LineTo(float32(q.X-o.X), float32(q.Y-o.Y))
	}
	z.ClosePath()
	mask := image.NewAlpha(r)
	z.Draw(mask, r, image.Opaque, image.Point{})
	cv.paint(mask, c)
}

// stroke paints a polyline of the given width with round caps and
// joins, as an anti-aliased distance field: coverage is how far a pixel
// centre sits inside width/2 of the nearest segment.
func (cv *canvas) stroke(pts []vec, width float64, c Color) {
	if len(pts) == 0 || width <= 0 {
		return
	}
	p := cv.toPx(pts)
	half := width * cv.px / 2
	r := boundsOf(p, half+2, cv.img.Rect)
	if r.Empty() {
		return
	}
	mask := image.NewAlpha(r)
	if len(p) == 1 {
		p = append(p, p[0])
	}
	for i := 0; i+1 < len(p); i++ {
		a, b := p[i], p[i+1]
		sr := boundsOf([]vec{a, b}, half+2, r)
		ab := b.sub(a)
		l2 := ab.dot(ab)
		for y := sr.Min.Y; y < sr.Max.Y; y++ {
			for x := sr.Min.X; x < sr.Max.X; x++ {
				q := vec{float64(x) + 0.5, float64(y) + 0.5}
				t := 0.0
				if l2 > 0 {
					t = math.Max(0, math.Min(1, q.sub(a).dot(ab)/l2))
				}
				cov := half - q.dist(a.add(ab.mul(t))) + 0.5
				if cov <= 0 {
					continue
				}
				v := uint8(math.Min(cov, 1) * 255)
				off := mask.PixOffset(x, y)
				if v > mask.Pix[off] {
					mask.Pix[off] = v
				}
			}
		}
	}
	cv.paint(mask, c)
}

// fillOutlined fills a polygon and strokes its outline with width w, so
// the shape grows by w/2 on every side (a border under the coloured fill).
func (cv *canvas) fillOutlined(pts []vec, w float64, c Color) {
	if len(pts) < 3 {
		return
	}
	cv.fill(pts, c)
	if w > 0 {
		cv.stroke(append(pts, pts[0]), w, c)
	}
}

// roundRect is a rounded rectangle as a closed polygon.
func roundRect(r frect, rad float64) []vec {
	rad = math.Max(0, math.Min(rad, math.Min(r.W, r.H)/2))
	if rad == 0 {
		return []vec{{r.minX(), r.minY()}, {r.maxX(), r.minY()}, {r.maxX(), r.maxY()}, {r.minX(), r.maxY()}}
	}
	corners := []struct {
		c     vec
		start float64
	}{
		{vec{r.maxX() - rad, r.minY() + rad}, -math.Pi / 2},
		{vec{r.maxX() - rad, r.maxY() - rad}, 0},
		{vec{r.minX() + rad, r.maxY() - rad}, math.Pi / 2},
		{vec{r.minX() + rad, r.minY() + rad}, math.Pi},
	}
	var out []vec
	for _, k := range corners {
		for i := 0; i <= 12; i++ {
			a := k.start + float64(i)/12*math.Pi/2
			out = append(out, k.c.add(vec{rad * math.Cos(a), rad * math.Sin(a)}))
		}
	}
	return out
}

func ellipse(c vec, rx, ry float64) []vec {
	out := make([]vec, 0, 97)
	for i := 0; i <= 96; i++ {
		a := float64(i) / 96 * 2 * math.Pi
		out = append(out, c.add(vec{rx * math.Cos(a), ry * math.Sin(a)}))
	}
	return out
}

// ---- text ------------------------------------------------------------

// drawFontCandidates are heavy, rounded-ish system fonts that read well
// on a sign, tried in order when no font is configured; the embedded Go
// Bold is the last resort, so rendering never fails on a bare system.
func drawFontCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"Arial Rounded Bold", "Arial Black", "Arial Bold"}
	case "windows":
		return []string{"seguibl", "ariblk", "segoeuib", "arialbd"}
	}
	return []string{"DejaVuSans-Bold"}
}

var (
	fontMu    sync.Mutex
	fontCache = map[string]*opentype.Font{}
)

// loadSignFont resolves name (path or system font name; "" = automatic)
// to a parsed font. An explicitly named font that cannot be loaded is an
// error, never silently replaced.
func loadSignFont(name string) (*opentype.Font, error) {
	fontMu.Lock()
	defer fontMu.Unlock()
	if f, ok := fontCache[name]; ok {
		return f, nil
	}
	var f *opentype.Font
	if name != "" {
		p, err := resolveFontPath(name)
		if err != nil {
			return nil, err
		}
		if f = parseFontFile(p); f == nil {
			return nil, fmt.Errorf("draw: %s is not a readable TrueType/OpenType font", p)
		}
	} else {
		for _, c := range drawFontCandidates() {
			if p, err := resolveFontPath(c); err == nil {
				if f = parseFontFile(p); f != nil {
					break
				}
			}
		}
		if f == nil {
			f, _ = opentype.Parse(gobold.TTF)
		}
	}
	fontCache[name] = f
	return f, nil
}

func parseFontFile(path string) *opentype.Font {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if f, err := opentype.Parse(b); err == nil {
		return f
	}
	if c, err := opentype.ParseCollection(b); err == nil && c.NumFonts() > 0 {
		f, _ := c.Font(0)
		return f
	}
	return nil
}

// signText is the sign's sentence laid out at one font size, in pixels.
type signText struct {
	face     font.Face
	fallback font.Face      // embedded Go Bold, for runes the sign font lacks
	symbols  []font.Face    // system symbol fonts, for runes Go Bold lacks too (✓ ✗ ★)
	font     *opentype.Font // the sign font, for glyph-coverage checks
	lines    []string
	widths   []float64
	lineH    float64
	ascent   float64
	w, h     float64
}

func layoutText(fnt *opentype.Font, text string, sizePx, maxWPx float64) signText {
	opts := &opentype.FaceOptions{Size: sizePx, DPI: 72, Hinting: font.HintingNone}
	face, _ := opentype.NewFace(fnt, opts)
	fb, _ := opentype.NewFace(fallbackFont(), opts)
	st0 := signText{face: face, fallback: fb, font: fnt}
	for _, sf := range symbolFonts() {
		f, _ := opentype.NewFace(sf, opts)
		st0.symbols = append(st0.symbols, f)
	}
	measure := func(s string) float64 {
		w := 0.0
		for _, r := range st0.runs(s) {
			w += float64(font.MeasureString(r.face, r.text)) / 64
		}
		return w
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		words := strings.Fields(para)
		cur := ""
		for _, w := range words {
			try := w
			if cur != "" {
				try = cur + " " + w
			}
			if cur != "" && measure(try) > maxWPx {
				lines = append(lines, cur)
				cur = w
			} else {
				cur = try
			}
		}
		lines = append(lines, cur)
	}
	met := face.Metrics()
	st := st0
	st.lines, st.lineH, st.ascent = lines, float64(met.Height)/64, float64(met.Ascent)/64
	glyphH := float64(met.Ascent+met.Descent) / 64
	for _, l := range lines {
		wd := measure(l)
		st.widths = append(st.widths, wd)
		st.w = math.Max(st.w, wd)
	}
	// Block height: every line's advance except the last, which only
	// needs its glyphs (no trailing line gap), so text centres optically.
	st.h = st.lineH*float64(len(lines)-1) + glyphH
	return st
}

type textRun struct {
	text string
	face font.Face
}

// runs splits s into maximal runs drawable by one face: the sign font
// where it has the glyph, the fallback where it does not (e.g. '·' or
// '→' in Arial Rounded Bold), so a sign never shows a missing-glyph box.
func (st signText) runs(s string) []textRun {
	var out []textRun
	var buf sfnt.Buffer
	for _, r := range s {
		f := st.face
		if idx, err := st.font.GlyphIndex(&buf, r); (err != nil || idx == 0) && r != ' ' && st.fallback != nil {
			f = st.fallback
			if idx, err := fallbackFont().GlyphIndex(&buf, r); err != nil || idx == 0 {
				for i, sf := range symbolFonts() {
					if idx, err := sf.GlyphIndex(&buf, r); err == nil && idx != 0 && i < len(st.symbols) {
						f = st.symbols[i]
						break
					}
				}
			}
		}
		if n := len(out); n > 0 && out[n-1].face == f {
			out[n-1].text += string(r)
		} else {
			out = append(out, textRun{string(r), f})
		}
	}
	return out
}

var (
	fallbackOnce sync.Once
	fallbackFnt  *opentype.Font
)

var (
	symbolOnce sync.Once
	symbolFnts []*opentype.Font
)

// symbolFonts are the OS's symbol fonts, tried in order per rune.
func symbolFonts() []*opentype.Font {
	symbolOnce.Do(func() {
		var names []string
		switch runtime.GOOS {
		case "darwin":
			names = []string{"ZapfDingbats", "Apple Symbols", "Menlo"}
		case "windows":
			names = []string{"seguisym"}
		default:
			names = []string{"DejaVuSans"}
		}
		for _, n := range names {
			if p, err := resolveFontPath(n); err == nil {
				if f := parseFontFile(p); f != nil {
					symbolFnts = append(symbolFnts, f)
				}
			}
		}
	})
	return symbolFnts
}

func fallbackFont() *opentype.Font {
	fallbackOnce.Do(func() { fallbackFnt, _ = opentype.Parse(gobold.TTF) })
	return fallbackFnt
}

// fitText shrinks the font until the text fits in drawMaxLines lines,
// never below the minimum size.
func fitText(fnt *opentype.Font, text string, m drawMetrics, ui, px, maxW float64) signText {
	size := m.fontSize
	for {
		st := layoutText(fnt, text, size*px, maxW*px)
		if len(st.lines) <= drawMaxLines || size <= drawMinFontSize*ui {
			return st
		}
		size = math.Max(size-2*ui, drawMinFontSize*ui)
	}
}

func (cv *canvas) text(st signText, box frect, c Color) {
	d := &font.Drawer{Dst: cv.img, Src: image.NewUniform(rgba(c)), Face: st.face}
	top := box.Y*cv.px + (box.H*cv.px-st.h)/2
	for i, l := range st.lines {
		x := box.X*cv.px + (box.W*cv.px-st.widths[i])/2
		y := top + float64(i)*st.lineH + st.ascent
		d.Dot = fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}
		for _, r := range st.runs(l) {
			d.Face = r.face
			d.DrawString(r.text)
		}
	}
}

// ---- composition ----------------------------------------------------

type drawLook struct {
	color, border, text, edge Color
	style                     DrawBorder
}

func lookOf(o DrawOptions) drawLook {
	c := *o.Color
	l := drawLook{color: c, border: c.contrast(), text: c.contrast(), edge: Color{}, style: o.Border}
	if o.Border == BorderBlack {
		l.edge = c.outline()
	}
	if o.BorderColor != nil {
		l.border = *o.BorderColor
	}
	if o.TextColor != nil {
		l.text = *o.TextColor
	}
	if o.EdgeColor != nil {
		l.edge = *o.EdgeColor
	}
	return l
}

// borders returns the (colour, extra width) layers drawn under each
// part, outermost first.
func (l drawLook) borders(ui float64) []struct {
	c Color
	w float64
} {
	type b = struct {
		c Color
		w float64
	}
	switch l.style {
	case BorderWhiteBlack:
		return []b{{l.edge, (drawOutlineWidth + drawEdgeWidth) * ui}, {l.border, drawOutlineWidth * ui}}
	case BorderBlack:
		return []b{{l.edge, drawEdgeWidth * ui}}
	}
	return []b{{l.border, drawOutlineWidth * ui}}
}

// renderLayout paints one drawing onto cv.
func renderLayout(cv *canvas, lay drawLayout, st signText, look drawLook) {
	renderLayoutPose(cv, lay, st, look, finalPose)
}

// renderLayoutPose paints one drawing in an animation pose; finalPose
// paints exactly what renderLayout always did.
func renderLayoutPose(cv *canvas, lay drawLayout, st signText, look drawLook, pose drawPose) {
	ui := lay.uiScale
	art := newCanvasIn(cv.img.Rect, cv.px)
	borders := look.borders(ui)
	a := pose.arrow(lay.arrow)
	stroke := lay.metrics.stroke

	// The target mark: border(s) then the coloured line. Outline only.
	var markPath []vec
	switch lay.mark.kind {
	case markBox:
		markPath = append(roundRect(lay.mark.rect, 12*ui), roundRect(lay.mark.rect, 12*ui)[0])
	case markRing:
		markPath = ellipse(lay.mark.center, lay.mark.rx, lay.mark.ry)
	}
	markPath = pose.markPath(markPath, lay.mark)
	if len(markPath) > 0 {
		for _, b := range borders {
			art.stroke(markPath, drawMarkLineWidth*ui+b.w, b.c)
		}
		art.stroke(markPath, drawMarkLineWidth*ui, look.color)
	}
	part := func(b struct {
		c Color
		w float64
	}) {
		art.stroke(a.shaft, stroke+b.w, b.c)
		art.fillOutlined(a.head, b.w, b.c)
		art.fillOutlined(a.root, b.w, b.c)
	}
	// Every black edge below every contrast border.
	for _, b := range borders[:len(borders)-1] {
		part(b)
	}
	// The sign goes on its own layer while it fades / slides in.
	sg, signDY := art, 0.0
	if !pose.signSettled() {
		sg, signDY = newCanvasIn(cv.img.Rect, cv.px), pose.signDY
	}
	sign := lay.sign
	sign.Y += signDY
	pill := roundRect(sign, lay.radius)
	if look.style != BorderShadow {
		sg.fill(pill, look.edge)
	}
	part(borders[len(borders)-1])
	// Sign body: fill + contrast border fully inside its rect.
	inset := 0.0
	if look.style != BorderShadow {
		inset = signEdgeLine*ui - 0.5*ui
	}
	body := sign.inset(inset, inset)
	rad := math.Max(0, lay.radius-inset)
	if look.style == BorderBlack {
		sg.fill(roundRect(body, rad), look.color)
	} else {
		sg.fill(roundRect(body, rad), look.border)
		in := body.inset(signOutline*ui, signOutline*ui)
		sg.fill(roundRect(in, math.Max(0, rad-signOutline*ui)), look.color)
	}
	if sg != art {
		sg.text(st, sign, look.text)
		draw.DrawMask(art.img, art.img.Rect, sg.img, sg.img.Rect.Min,
			image.NewUniform(color.Alpha{uint8(math.Round(clamp01(pose.signAlpha) * 255))}), image.Point{}, draw.Over)
	}
	if len(a.shaft) > 0 {
		art.stroke(a.shaft, stroke, look.color)
	}
	art.fill(a.root, look.color)
	art.fill(a.head, look.color)
	if sg == art {
		art.text(st, sign, look.text)
	}

	if look.style == BorderShadow {
		shadow(cv, art, ui)
	}
	draw.Draw(cv.img, cv.img.Rect, art.img, cv.img.Rect.Min, draw.Over)
}

// shadow paints a soft drop shadow of art's alpha onto cv: a 3-pass box
// blur (≈ Gaussian) offset straight down.
func shadow(cv, art *canvas, ui float64) {
	sigma := drawShadowRadius / 2 * ui * cv.px
	drop := int(math.Round(drawShadowDrop * ui * cv.px))
	reach := int(math.Ceil(sigma * 3))
	src := art.img
	used := opaqueBounds(src)
	if used.Empty() {
		return
	}
	r := used.Inset(-reach - 2).Add(image.Pt(0, drop)).Intersect(cv.img.Rect)
	w, h := r.Dx(), r.Dy()
	buf := make([]float64, w*h)
	for y := 0; y < h; y++ {
		sy := r.Min.Y + y - drop
		if sy < src.Rect.Min.Y || sy >= src.Rect.Max.Y {
			continue
		}
		for x := 0; x < w; x++ {
			buf[y*w+x] = float64(src.Pix[src.PixOffset(r.Min.X+x, sy)+3]) / 255
		}
	}
	box := int(math.Max(1, math.Round(sigma*math.Sqrt(3))))
	tmp := make([]float64, len(buf))
	for pass := 0; pass < 3; pass++ {
		blur1D(buf, tmp, w, h, box, true)
		blur1D(tmp, buf, w, h, box, false)
	}
	mask := image.NewAlpha(r)
	for i, v := range buf {
		mask.Pix[i] = uint8(math.Min(1, v*drawShadowOpacity) * 255)
	}
	cv.paint(mask, Color{})
}

func blur1D(src, dst []float64, w, h, rad int, horizontal bool) {
	n, m := w, h
	at := func(i, j int) int { return j*w + i }
	if !horizontal {
		n, m = h, w
		at = func(i, j int) int { return i*w + j }
	}
	norm := 1 / float64(2*rad+1)
	for j := 0; j < m; j++ {
		sum := 0.0
		for i := -rad; i <= rad; i++ {
			if i >= 0 && i < n {
				sum += src[at(i, j)]
			}
		}
		for i := 0; i < n; i++ {
			dst[at(i, j)] = sum * norm
			if k := i + rad + 1; k < n {
				sum += src[at(k, j)]
			}
			if k := i - rad; k >= 0 {
				sum -= src[at(k, j)]
			}
		}
	}
}

func opaqueBounds(img *image.RGBA) image.Rectangle {
	b := img.Rect
	x0, y0, x1, y1 := b.Max.X, b.Max.Y, b.Min.X, b.Min.Y
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[img.PixOffset(b.Min.X, y) : img.PixOffset(b.Min.X, y)+b.Dx()*4]
		for x := 0; x < b.Dx(); x++ {
			if row[x*4+3] != 0 {
				x0, x1 = min(x0, b.Min.X+x), max(x1, b.Min.X+x+1)
				y0, y1 = min(y0, y), max(y1, y+1)
			}
		}
	}
	return image.Rect(x0, y0, x1, y1)
}
