package engine

// Text-only drawings (`draw --style text`) and subtitles: a styled label
// on a pill / card, no arrow, placed at one of nine positions inside a
// container (a window, a rect, a monitor) or beside an anchor point.
//
// Default placement follows the film / broadcast subtitle convention
// (Netflix and BBC title-safe areas): bottom-center, the text block
// lifted 8% of the container height off the bottom edge, 5% side safe
// area, lines at most 80% of the container width, at most two lines
// before the font shrinks. Subtitles size their font at ~4.75% of the
// container height (clamped 18..64 units); `draw text` defaults to M.
// Pure Go like the rest of draw: identical pixels on every OS.

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// DrawText is the text-only style: a label, no arrow.
const DrawText DrawStyle = "text"

// SizeXL is the extra-large size preset.
const SizeXL DrawSize = "XL"

// DrawPosition is where a text drawing sits inside its container (or
// beside its anchor point). For arrow / ring / box drawings it is an
// alias of From: the side of the target the sign sits on.
type DrawPosition string

// textPositions maps each position to its (horizontal, vertical) cell:
// -1 left/top, 0 centre, 1 right/bottom.
var textPositions = map[DrawPosition][2]int{
	"center": {0, 0}, "top": {0, -1}, "top-center": {0, -1}, "bottom": {0, 1}, "bottom-center": {0, 1},
	"left": {-1, 0}, "right": {1, 0}, "top-left": {-1, -1}, "top-right": {1, -1},
	"bottom-left": {-1, 1}, "bottom-right": {1, 1},
}

var textPositionNames = []string{"bottom-center", "top-center", "center", "left", "right", "top-left", "top-right", "bottom-left", "bottom-right", "top", "bottom"}

// Subtitle-convention defaults.
const (
	textMarginV      = 0.08 // of container height, top / bottom
	textMarginH      = 0.05 // of container width, left / right
	textMaxWidth     = 0.80 // of container width
	textMaxLines     = 2
	textPointMargin  = 12.0 // units, beside an anchor point
	subtitleFontPart = 0.0475
	subtitleFontMin  = 18.0
	subtitleFontMax  = 64.0
	textMinFont      = 14.0
	textBgAlpha      = 0.78 // auto background
)

// textFontSizes are the S/M/L/XL text sizes in layout units.
func textFontSize(s DrawSize) float64 {
	switch s {
	case SizeS:
		return 24
	case SizeL:
		return 48
	case SizeXL:
		return 72
	}
	return 34
}

// textScene is a placed text drawing.
type textScene struct {
	box     frect // the card, surface-local units
	content frect // the text block inside it
	st      signText
	align   string
	fg      Color
	bg      *Color // nil = no background
	bgAlpha float64
	radius  float64
	shadow  bool
	ui      float64
}

// parseLength reads "32" (units) or "8%" (of ref).
func parseLength(raw string, ref float64) (float64, error) {
	raw = strings.TrimSpace(raw)
	if p, ok := strings.CutSuffix(raw, "%"); ok {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 {
			return 0, fmt.Errorf("%q is not a percentage", raw)
		}
		return ref * v / 100, nil
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(raw, "px"), 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%q is not a length (px or %%)", raw)
	}
	return v, nil
}

// textDefaults fills the text / subtitle defaults the config did not.
func (o *DrawOptions) textDefaults() error {
	if o.Position == "" {
		o.Position = "bottom-center"
	}
	p := DrawPosition(strings.ToLower(string(o.Position)))
	if _, ok := textPositions[p]; !ok {
		return fmt.Errorf("draw: position %q is not one of %s", o.Position, strings.Join(textPositionNames, ", "))
	}
	o.Position = p
	if o.Color == nil {
		o.Color = &whiteColor
	}
	if o.Background == "" {
		o.Background = "auto"
	}
	switch strings.ToLower(o.Align) {
	case "":
		o.Align = "center"
		if h := textPositions[p][0]; h < 0 {
			o.Align = "left"
		} else if h > 0 {
			o.Align = "right"
		}
	case "left", "center", "right":
		o.Align = strings.ToLower(o.Align)
	default:
		return fmt.Errorf("draw: align %q is not one of left, center, right", o.Align)
	}
	switch strings.ToLower(o.Weight) {
	case "":
		o.Weight = "bold"
	case "regular", "bold":
		o.Weight = strings.ToLower(o.Weight)
	default:
		return fmt.Errorf("draw: weight %q is not one of regular, bold", o.Weight)
	}
	return nil
}

func (o DrawOptions) textBackground() (*Color, float64, error) {
	switch strings.ToLower(o.Background) {
	case "none", "transparent", "off":
		return nil, 0, nil
	case "auto", "":
		c := o.Color.contrast()
		return &c, textBgAlpha, nil
	}
	c, err := ParseColor(o.Background)
	if err != nil {
		return nil, 0, fmt.Errorf("draw: --bg: %w", err)
	}
	return &c, 1, nil
}

func textFontCandidates(weight string) []string {
	if weight != "regular" {
		return drawFontCandidates()
	}
	switch runtime.GOOS {
	case "darwin":
		return []string{"Arial", "Helvetica"}
	case "windows":
		return []string{"segoeui", "arial"}
	}
	return []string{"DejaVuSans"}
}

func loadTextFont(name, weight string) (*opentype.Font, error) {
	if name != "" || weight != "regular" {
		return loadSignFont(name)
	}
	key := "\x00regular"
	fontMu.Lock()
	defer fontMu.Unlock()
	if f, ok := fontCache[key]; ok {
		return f, nil
	}
	var f *opentype.Font
	for _, c := range textFontCandidates(weight) {
		if p, err := resolveFontPath(c); err == nil {
			if f = parseFontFile(p); f != nil {
				break
			}
		}
	}
	if f == nil {
		f, _ = opentype.Parse(goregular.TTF)
	}
	fontCache[key] = f
	return f, nil
}

// planTextScene places a text drawing on its monitor.
func planTextScene(a Adapter, opts DrawOptions, sizeGiven bool) (*drawScene, error) {
	if err := opts.textDefaults(); err != nil {
		return nil, err
	}
	n := 0
	for _, set := range []bool{opts.Point != nil, opts.Region != nil, opts.Match != nil, opts.Mouse} {
		if set {
			n++
		}
	}
	var container frect
	var isPoint bool
	var mon Monitor
	switch n {
	case 0: // a monitor: --monitor N, else the one with focus
		ms, err := listMonitorsWith(a)
		if err != nil {
			return nil, err
		}
		if len(ms) == 0 {
			return nil, errors.New("draw: no monitors reported")
		}
		mon = defaultScreenshotMonitor(ms)
		if opts.Monitor != nil {
			if mon, err = findMonitor(ms, *opts.Monitor); err != nil {
				return nil, err
			}
		}
		container = frect{float64(mon.X), float64(mon.Y), float64(mon.Width), float64(mon.Height)}
	case 1:
		var err error
		if container, isPoint, mon, err = resolveDrawTarget(a, opts); err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("draw: give at most one place for the text: --app/--title, --rect, --at, --mouse, or just --monitor")
	}
	surface := Rect{X: mon.X, Y: mon.Y, W: mon.Width, H: mon.Height}
	ui, px := a.OverlayScale(surface)
	if ui <= 0 {
		ui = 1
	}
	if px <= 0 {
		px = 1
	}
	bounds := frect{0, 0, float64(mon.Width), float64(mon.Height)}
	c := frect{container.X - float64(mon.X), container.Y - float64(mon.Y), container.W, container.H}
	// A window reaching past its monitor is clipped to it.
	if !isPoint {
		if in, ok := c.intersect(bounds); ok {
			c = in
		} else {
			c = bounds
		}
	}
	ref := c // what percentages refer to
	if isPoint {
		ref = bounds
	}

	// Margins: px or %, default the subtitle safe area.
	mx, my := ref.W*textMarginH, ref.H*textMarginV
	if isPoint {
		mx, my = textPointMargin*ui, textPointMargin*ui
	}
	if opts.Margin != "" {
		var err error
		raw := strings.TrimSpace(opts.Margin)
		if strings.HasSuffix(raw, "%") {
			if mx, err = parseLength(raw, ref.W); err == nil {
				my, err = parseLength(raw, ref.H)
			}
		} else if mx, err = parseLength(raw, 0); err == nil {
			mx *= ui
			my = mx
		}
		if err != nil {
			return nil, fmt.Errorf("draw: --margin %w", err)
		}
	}
	maxW := ref.W * textMaxWidth
	if opts.MaxWidth != "" {
		v, err := parseLength(opts.MaxWidth, ref.W)
		if err != nil {
			return nil, fmt.Errorf("draw: --max-width %w", err)
		}
		if !strings.HasSuffix(strings.TrimSpace(opts.MaxWidth), "%") {
			v *= ui
		}
		maxW = v
	}
	maxW = math.Max(80*ui, math.Min(maxW, bounds.W-2*mx))

	// Font size: explicit > subtitle (share of container height) > S/M/L/XL.
	size := textFontSize(opts.Size) * ui
	switch {
	case opts.FontSize > 0:
		size = opts.FontSize * ui
	case opts.Subtitle && !sizeGiven:
		size = math.Max(subtitleFontMin*ui, math.Min(subtitleFontMax*ui, ref.H*subtitleFontPart))
	}
	if opts.FontScale > 0 {
		size *= opts.FontScale
	}
	fnt, err := loadTextFont(opts.Font, opts.Weight)
	if err != nil {
		return nil, err
	}
	padX, padY := 0.6*size, 0.32*size
	maxLines := opts.MaxLines
	if maxLines <= 0 {
		maxLines = textMaxLines
	}
	// Shrink until it wraps into maxLines (explicit newlines always count).
	paras := len(strings.Split(opts.Text, "\n"))
	st := layoutText(fnt, opts.Text, size*px, (maxW-2*padX)*px)
	for len(st.lines) > max(maxLines, paras) && size > textMinFont*ui {
		size = math.Max(size-2*ui, textMinFont*ui)
		padX, padY = 0.6*size, 0.32*size
		st = layoutText(fnt, opts.Text, size*px, (maxW-2*padX)*px)
	}
	bg, bgAlpha, err := opts.textBackground()
	if err != nil {
		return nil, err
	}
	if bg == nil {
		padX, padY = 0.15*size, 0.1*size
	}
	w := math.Ceil(st.w/px + 2*padX)
	h := math.Ceil(st.h/px + 2*padY)

	pos := textPositions[opts.Position]
	var box frect
	if isPoint {
		ax, ay := c.X, c.Y
		box.X = ax - w/2 + float64(pos[0])*(w/2+mx)
		box.Y = ay - h/2 + float64(pos[1])*(h/2+my)
	} else {
		in := frect{c.X + mx, c.Y + my, c.W - 2*mx, c.H - 2*my}
		place := func(lo, span, size float64, cell int) float64 {
			switch {
			case size > span || cell == 0:
				return lo + (span-size)/2
			case cell < 0:
				return lo
			}
			return lo + span - size
		}
		box.X = place(in.X, in.W, w, pos[0])
		box.Y = place(in.Y, in.H, h, pos[1])
	}
	box.W, box.H = w, h
	if opts.Offset != nil {
		box.X += float64(opts.Offset.X)
		box.Y += float64(opts.Offset.Y)
	}
	box = box.clampedInto(bounds)
	box.X, box.Y = math.Round(box.X), math.Round(box.Y)

	radius := math.Min(h/2, 0.55*size)
	if opts.Corners == CornersSharp {
		radius = 4 * ui
	}
	ts := &textScene{
		box: box, content: box.inset(padX, padY), st: st, align: opts.Align,
		fg: *opts.Color, bg: bg, bgAlpha: bgAlpha, radius: radius,
		shadow: opts.Border == BorderShadow || opts.Border == "", ui: ui,
	}
	if opts.TextColor != nil {
		ts.fg = *opts.TextColor
	}
	anchor := box.center()
	if isPoint {
		anchor = vec{c.X, c.Y}
	}
	res := DrawResult{
		Monitor:    mon.ID,
		Sign:       Rect{X: mon.X + int(box.X), Y: mon.Y + int(box.Y), W: int(box.W), H: int(box.H)},
		Tip:        Point{mon.X + int(math.Round(anchor.X)), mon.Y + int(math.Round(anchor.Y))},
		From:       string(opts.Position),
		Surface:    surface,
		PixelScale: px,
	}
	return &drawScene{
		opts: opts, text: ts, px: px, res: res,
		lay:     drawLayout{sign: box, uiScale: ui, anchor: anchor},
		surface: image.Rect(0, 0, int(math.Ceil(bounds.W*px)), int(math.Ceil(bounds.H*px))),
	}, nil
}

// renderTextPose paints the label, faded / slid by the pose.
func renderTextPose(cv *canvas, t *textScene, pose drawPose) {
	art := newCanvasIn(cv.img.Rect, cv.px)
	box, content := t.box, t.content
	box.Y += pose.signDY
	content.Y += pose.signDY
	if t.bg != nil {
		art.fillAlpha(roundRect(box, t.radius), *t.bg, t.bgAlpha)
	} else {
		// No card: a thin contrasting halo keeps the text readable.
		halo := t.fg.contrast()
		r := math.Max(1, 1.5*t.ui)
		for _, d := range []vec{{-r, 0}, {r, 0}, {0, -r}, {0, r}, {-r, -r}, {r, r}, {-r, r}, {r, -r}} {
			art.textAligned(t.st, content.add(d), t.align, halo)
		}
	}
	art.textAligned(t.st, content, t.align, t.fg)
	if t.shadow {
		shadow(cv, art, t.ui)
	}
	a := clamp01(pose.signAlpha)
	draw.DrawMask(cv.img, cv.img.Rect, art.img, cv.img.Rect.Min,
		image.NewUniform(color.Alpha{uint8(math.Round(a * 255))}), image.Point{}, draw.Over)
}

func (r frect) add(d vec) frect { return frect{r.X + d.X, r.Y + d.Y, r.W, r.H} }

// fillAlpha fills a polygon at opacity a.
func (cv *canvas) fillAlpha(pts []vec, c Color, a float64) {
	if a >= 1 {
		cv.fill(pts, c)
		return
	}
	layer := newCanvasIn(cv.img.Rect, cv.px)
	layer.fill(pts, c)
	draw.DrawMask(cv.img, cv.img.Rect, layer.img, cv.img.Rect.Min,
		image.NewUniform(color.Alpha{uint8(math.Round(clamp01(a) * 255))}), image.Point{}, draw.Over)
}

// textAligned draws st's lines inside box, each aligned left / center /
// right, the block centred vertically.
func (cv *canvas) textAligned(st signText, box frect, align string, c Color) {
	d := &font.Drawer{Dst: cv.img, Src: image.NewUniform(rgba(c)), Face: st.face}
	top := box.Y*cv.px + (box.H*cv.px-st.h)/2
	for i, l := range st.lines {
		x := box.X * cv.px
		switch align {
		case "center":
			x += (box.W*cv.px - st.widths[i]) / 2
		case "right":
			x += box.W*cv.px - st.widths[i]
		}
		y := top + float64(i)*st.lineH + st.ascent
		d.Dot = fixed.Point26_6{X: fixed.Int26_6(x * 64), Y: fixed.Int26_6(y * 64)}
		for _, r := range st.runs(l) {
			d.Face = r.face
			d.DrawString(r.text)
		}
	}
}
