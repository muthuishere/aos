package engine

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/engine/core"
)

// A cover is a draw overlay that hides instead of pointing: an opaque box
// (any colour, optional text and border) over a rect, a window, part of a
// window, or text found in a window. It follows its window and never lets
// the area underneath show while it is up. See docs/plans/118-draw-cover.md.

// CoverStyle is how a cover looks. Every field is optional; the JSON form
// is also a preset in the draw config (`presets: {private: {...}}`) and the
// patch `draw cover update` sends to a live cover.
type CoverStyle struct {
	// Color fills the box: a preset name, #RGB, #RRGGBB or #RRGGBBAA.
	// Default opaque black. Alpha below 1 makes a tint, which is never
	// counted as redaction.
	Color string `json:"color,omitempty"`
	// Text is drawn inside the box (\n breaks lines); it shrinks to fit
	// and never spills out of the cover.
	Text      string  `json:"text,omitempty"`
	TextColor string  `json:"textColor,omitempty"` // default: readable contrast of Color
	Font      string  `json:"font,omitempty"`
	FontScale float64 `json:"fontScale,omitempty"`
	Size      string  `json:"size,omitempty"`     // S, M (default), L, XL: the largest the text may be
	Weight    string  `json:"weight,omitempty"`   // regular, bold (default)
	Align     string  `json:"align,omitempty"`    // left, center (default), right
	Position  string  `json:"position,omitempty"` // center (default), top-left, bottom, ...
	Margin    string  `json:"margin,omitempty"`   // px or N% of the box; default 6% of the short side

	Border      float64 `json:"border,omitempty"` // px; 0 = none
	BorderColor string  `json:"borderColor,omitempty"`
	Radius      float64 `json:"radius,omitempty"` // corner radius, px
}

// Merge returns s with every field p sets laid over it.
func (s CoverStyle) Merge(p CoverStyle) CoverStyle {
	str := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	num := func(dst *float64, v float64) {
		if v != 0 {
			*dst = v
		}
	}
	str(&s.Color, p.Color)
	str(&s.Text, p.Text)
	str(&s.TextColor, p.TextColor)
	str(&s.Font, p.Font)
	num(&s.FontScale, p.FontScale)
	str(&s.Size, p.Size)
	str(&s.Weight, p.Weight)
	str(&s.Align, p.Align)
	str(&s.Position, p.Position)
	str(&s.Margin, p.Margin)
	num(&s.Border, p.Border)
	str(&s.BorderColor, p.BorderColor)
	num(&s.Radius, p.Radius)
	return s
}

// ErrCoverTint is returned when a cover with alpha below 1 is used where
// redaction is required: a tint lets the content show through.
var ErrCoverTint = errors.New("a tint is not redaction: a cover colour with alpha below 1 lets the content show through; use an opaque colour")

// ParseCoverColor reads a preset name, #RGB, #RRGGBB or #RRGGBBAA and
// returns the colour and its alpha (1 unless #RRGGBBAA says less).
// "black" is #000000.
func ParseCoverColor(raw string) (Color, float64, error) {
	digits := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "#")
	if digits == "black" {
		return Color{}, 1, nil // a cover's black is true black, not the sign ink
	}
	if len(digits) == 8 {
		a, err := strconv.ParseUint(digits[6:], 16, 8)
		if err != nil {
			return Color{}, 0, fmt.Errorf("colour %q is not valid #RRGGBBAA", raw)
		}
		c, err := ParseColor(digits[:6])
		return c, float64(a) / 255, err
	}
	c, err := ParseColor(raw)
	return c, 1, err
}

// coverLook is a validated CoverStyle.
type coverLook struct {
	fill        Color
	alpha       float64
	text        string
	textColor   Color
	font        string
	fontScale   float64
	size        DrawSize
	weight      string
	align       string
	position    [2]int
	margin      string
	border      float64
	borderColor Color
	radius      float64
}

// Tint reports whether the style lets the content show through.
func (s CoverStyle) Tint() (bool, error) {
	l, err := s.look()
	return err == nil && l.alpha < 1, err
}

func (s CoverStyle) look() (coverLook, error) {
	l := coverLook{fill: Color{}, alpha: 1, text: strings.ReplaceAll(s.Text, `\n`, "\n"), font: s.Font,
		fontScale: s.FontScale, weight: strings.ToLower(s.Weight), margin: s.Margin, border: s.Border, radius: s.Radius}
	if s.Color != "" {
		c, a, err := ParseCoverColor(s.Color)
		if err != nil {
			return l, fmt.Errorf("cover --color: %w", err)
		}
		l.fill, l.alpha = c, a
	}
	l.textColor = l.fill.contrast()
	if s.TextColor != "" {
		c, err := ParseColor(s.TextColor)
		if err != nil {
			return l, fmt.Errorf("cover --text-color: %w", err)
		}
		l.textColor = c
	}
	l.borderColor = l.fill.contrast()
	if s.BorderColor != "" {
		c, err := ParseColor(s.BorderColor)
		if err != nil {
			return l, fmt.Errorf("cover --border-color: %w", err)
		}
		l.borderColor = c
	}
	l.size = DrawSize(strings.ToUpper(s.Size))
	switch l.size {
	case "":
		l.size = SizeM
	case SizeS, SizeM, SizeL, SizeXL:
	default:
		return l, fmt.Errorf("cover --size %q is not one of S, M, L, XL", s.Size)
	}
	switch l.weight {
	case "", "bold", "regular":
	default:
		return l, fmt.Errorf("cover --weight %q is not regular or bold", s.Weight)
	}
	l.align = strings.ToLower(s.Align)
	switch l.align {
	case "":
		l.align = "center"
	case "left", "center", "right":
	default:
		return l, fmt.Errorf("cover --align %q is not left, center or right", s.Align)
	}
	pos := DrawPosition(strings.ToLower(s.Position))
	if pos == "" {
		pos = "center"
	}
	p, ok := textPositions[pos]
	if !ok {
		return l, fmt.Errorf("cover --position %q is not one of %s", s.Position, strings.Join(textPositionNames, ", "))
	}
	l.position = p
	if l.fontScale < 0 || l.border < 0 || l.radius < 0 {
		return l, errors.New("cover: --font-scale, --border and --radius cannot be negative")
	}
	return l, nil
}

// coverMinFont is the smallest text size, in px, auto-fit shrinks to;
// text that still does not fit is cut off at the cover's edge.
const coverMinFont = 6.0

// renderCover paints a w x h (units) cover at pixel scale px. holes are
// box-local rects left transparent: parts of the target hidden behind
// other windows, which the cover must not paint over.
func renderCover(l coverLook, w, h, px float64, holes []frect) *image.RGBA {
	pw, ph := max(1, int(math.Ceil(w*px))), max(1, int(math.Ceil(h*px)))
	cv := newCanvas(pw, ph, px)
	box := frect{0, 0, float64(pw) / px, float64(ph) / px}
	fill := color.RGBA{uint8(l.fill.R*l.alpha*255 + 0.5), uint8(l.fill.G*l.alpha*255 + 0.5), uint8(l.fill.B*l.alpha*255 + 0.5), uint8(l.alpha*255 + 0.5)}
	if l.radius > 0 {
		cv.fillAlpha(roundRect(box, l.radius), l.fill, l.alpha)
	} else {
		// Exact, not anti-aliased: every pixel of the box is the fill.
		draw.Draw(cv.img, cv.img.Rect, image.NewUniform(fill), image.Point{}, draw.Src)
	}
	if l.border > 0 {
		pts := roundRect(box.inset(l.border/2, l.border/2), math.Max(0, l.radius-l.border/2))
		cv.stroke(append(pts, pts[0]), l.border, l.borderColor)
	}
	if strings.TrimSpace(l.text) != "" {
		drawCoverText(cv, l, box)
	}
	for _, hole := range holes {
		cutHole(cv.img, hole, px)
	}
	return cv.img
}

// coverHoleRadius is the corner radius kept covered in each hole: a
// window's bounds include its transparent rounded corners, through which
// the covered window would show. Larger than any macOS / Windows window
// corner, so the most it does is overdraw a corner of the front window.
const coverHoleRadius = 28.0

// cutHole makes the pixels inside hole (units) transparent, rounded
// inwards and with rounded corners left covered.
func cutHole(img *image.RGBA, hole frect, px float64) {
	r := image.Rect(int(math.Ceil(hole.X*px)), int(math.Ceil(hole.Y*px)),
		int(math.Floor(hole.maxX()*px)), int(math.Floor(hole.maxY()*px))).Intersect(img.Rect)
	rad := math.Min(coverHoleRadius, math.Min(hole.W, hole.H)/2) * px
	x0, y0, x1, y1 := hole.X*px, hole.Y*px, hole.maxX()*px, hole.maxY()*px
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			// The pixel's farthest corner from the hole's nearest corner centre.
			cx := math.Max(x0+rad, math.Min(x1-rad, float64(x)+0.5))
			cy := math.Max(y0+rad, math.Min(y1-rad, float64(y)+0.5))
			dx := math.Max(math.Abs(float64(x)-cx), math.Abs(float64(x+1)-cx))
			dy := math.Max(math.Abs(float64(y)-cy), math.Abs(float64(y+1)-cy))
			inCorner := (float64(x)+0.5 < x0+rad || float64(x)+0.5 > x1-rad) && (float64(y)+0.5 < y0+rad || float64(y)+0.5 > y1-rad)
			if inCorner && dx*dx+dy*dy > rad*rad {
				continue
			}
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0, 0, 0, 0
		}
	}
}

// drawCoverText lays the text out inside box: the largest size up to
// --size that fits the box less its margin, at --position.
func drawCoverText(cv *canvas, l coverLook, box frect) {
	short := math.Min(box.W, box.H)
	mx, my := math.Max(2, 0.06*short), math.Max(2, 0.06*short)
	if l.margin != "" {
		raw := strings.TrimSpace(l.margin)
		if x, err := parseLength(raw, box.W); err == nil {
			mx = x
			if y, err := parseLength(raw, box.H); err == nil && strings.HasSuffix(raw, "%") {
				my = y
			} else {
				my = x
			}
		}
	}
	mx = math.Min(mx, box.W/2-1)
	my = math.Min(my, box.H/2-1)
	if l.border > 0 {
		mx, my = mx+l.border, my+l.border
	}
	avail := frect{box.X + mx, box.Y + my, box.W - 2*mx, box.H - 2*my}
	if avail.W <= 1 || avail.H <= 1 {
		return
	}
	fnt, err := loadTextFont(l.font, l.weight)
	if err != nil || fnt == nil {
		return
	}
	size := textFontSize(l.size)
	if l.fontScale > 0 {
		size *= l.fontScale
	}
	px := cv.px
	st := layoutText(fnt, l.text, size*px, avail.W*px)
	for (st.w > avail.W*px || st.h > avail.H*px) && size > coverMinFont {
		size = math.Max(coverMinFont, size*0.9)
		st = layoutText(fnt, l.text, size*px, avail.W*px)
	}
	bw, bh := math.Min(st.w/px, avail.W), st.h/px
	place := func(lo, span, size float64, cell int) float64 {
		switch {
		case size >= span || cell == 0:
			return lo + (span-size)/2
		case cell < 0:
			return lo
		}
		return lo + span - size
	}
	block := frect{place(avail.X, avail.W, bw, l.position[0]), place(avail.Y, avail.H, bh, l.position[1]), bw, bh}
	cv.textAligned(st, block, l.align, l.textColor)
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RenderCoverImage renders a cover of w x h units at pixel scale px, as
// it is shown on screen and burned into a screenshot.
func RenderCoverImage(s CoverStyle, w, h, px float64) (*image.RGBA, error) {
	l, err := s.look()
	if err != nil {
		return nil, err
	}
	return renderCover(l, w, h, px, nil), nil
}

// CoverRect is a rect whose parts are units or N% of a container (the
// window for a window cover, the monitor with --monitor).
type CoverRect struct {
	X, Y, W, H coverLen
}

type coverLen struct {
	V   float64
	Pct bool
}

// ParseCoverRect reads "x,y,w,h"; each part is a number or N%.
func ParseCoverRect(raw string) (CoverRect, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 4 {
		return CoverRect{}, fmt.Errorf("--rect needs x,y,w,h, got %q", raw)
	}
	var v [4]coverLen
	for i, p := range parts {
		p = strings.TrimSpace(p)
		num, pct := strings.CutSuffix(p, "%")
		f, err := strconv.ParseFloat(strings.TrimSuffix(num, "px"), 64)
		if err != nil {
			return CoverRect{}, fmt.Errorf("--rect: %q is not a number or N%%", p)
		}
		v[i] = coverLen{f, pct}
	}
	r := CoverRect{v[0], v[1], v[2], v[3]}
	if r.W.V <= 0 || r.H.V <= 0 {
		return CoverRect{}, fmt.Errorf("--rect needs a positive width and height, got %q", raw)
	}
	return r, nil
}

func (r CoverRect) hasPct() bool { return r.X.Pct || r.Y.Pct || r.W.Pct || r.H.Pct }

// in resolves r inside the container c (global units).
func (r CoverRect) in(c Rect) Rect {
	at := func(l coverLen, ref float64) float64 {
		if l.Pct {
			return ref * l.V / 100
		}
		return l.V
	}
	x0 := float64(c.X) + at(r.X, float64(c.W))
	y0 := float64(c.Y) + at(r.Y, float64(c.H))
	return Rect{X: int(math.Floor(x0)), Y: int(math.Floor(y0)),
		W: int(math.Ceil(x0+at(r.W, float64(c.W))) - math.Floor(x0)),
		H: int(math.Ceil(y0+at(r.H, float64(c.H))) - math.Floor(y0))}
}

// CoverOptions describes one cover. Exactly one target: Rect (global, or
// monitor-relative with Monitor), Monitor alone (the whole monitor), or
// Match (the whole window; with Rect a part of it, window-relative; with
// Find the text found in it, grown by Pad).
type CoverOptions struct {
	Rect    *CoverRect
	Monitor *int
	Match   *Match

	Find       string
	FindSource FindSource
	FindRole   string
	FindExact  bool
	FindNth    int
	Pad        int

	Style CoverStyle
	// Preset names a style in the draw config's presets; Style is laid
	// over it.
	Preset   string
	NoConfig bool

	// Follow keeps a window cover on its window: it moves and resizes
	// with it, hides while the window is off screen and leaves out the
	// parts of other windows in front of it. On by default for window
	// targets; NoFollow pins the cover where the window was.
	NoFollow   bool
	BlockInput bool
	ID         string

	Duration time.Duration // 0 = until Stop
	Stop     <-chan struct{}
	// Updates, when set, is polled every tick; a non-nil style is merged
	// into the cover's style and shown in one frame.
	Updates func() *CoverStyle
	// UpdateShown is called once an update from Updates is on screen
	// (or was rejected and the old look kept).
	UpdateShown func()
	// Moved is called whenever the cover's place or visibility changes.
	Moved func(CoverResult)
}

// CoverResult says where a cover is, in global units.
type CoverResult struct {
	ID         string     `json:"ID,omitempty"`
	Rect       Rect       `json:"Rect"`
	Monitor    int        `json:"Monitor"`
	Window     *Window    `json:"Window,omitempty"`
	Follow     bool       `json:"Follow"`
	Hidden     bool       `json:"Hidden,omitempty"`
	BlockInput bool       `json:"BlockInput,omitempty"`
	Tint       bool       `json:"Tint,omitempty"`
	Style      CoverStyle `json:"Style"`
	Found      *FoundText `json:"Found,omitempty"`
}

// coverPlan is a resolved cover: where it goes now and how to re-place
// it when its window moves.
type coverPlan struct {
	opts  CoverOptions
	style CoverStyle
	look  coverLook
	win   *Window
	// place maps the window's bounds to the cover rect.
	place func(win Rect) Rect
	rect  Rect
	found *FoundText
}

func (o CoverOptions) style() (CoverStyle, error) {
	s := CoverStyle{}
	if !o.NoConfig {
		cfg, err := LoadDrawConfig()
		if err != nil {
			return s, err
		}
		if o.Preset != "" {
			presets := coverPresets(cfg)
			p, ok := presets[o.Preset]
			if !ok {
				return s, fmt.Errorf("cover: no preset %q (have: %s; add more under \"presets\" in %s)", o.Preset, presetNames(presets), DrawConfigPath())
			}
			s = p
		}
		if s.Font == "" && cfg.Text != nil {
			s.Font = cfg.Text.Font
		}
		if s.Font == "" {
			s.Font = cfg.Font
		}
	} else if o.Preset != "" {
		p, ok := BuiltinCoverPresets()[o.Preset]
		if !ok {
			return s, fmt.Errorf("cover: no built-in preset %q (have: %s)", o.Preset, presetNames(BuiltinCoverPresets()))
		}
		s = p
	}
	return s.Merge(o.Style), nil
}

// coverPresets are the built-in presets with the config's laid over them.
func coverPresets(c DrawConfig) map[string]CoverStyle {
	out := BuiltinCoverPresets()
	for n, p := range c.Presets {
		out[n] = p
	}
	return out
}

func presetNames(m map[string]CoverStyle) string {
	var names []string
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func planCoverWith(a Adapter, o CoverOptions) (*coverPlan, error) {
	style, err := o.style()
	if err != nil {
		return nil, err
	}
	l, err := style.look()
	if err != nil {
		return nil, err
	}
	p := &coverPlan{opts: o, style: style, look: l}
	switch {
	case o.Match != nil:
		if o.Monitor != nil {
			return nil, errors.New("cover: --monitor does not go with a window target")
		}
		w, err := matchOne(a, *o.Match)
		if err != nil {
			return nil, err
		}
		p.win = &w
		switch {
		case o.Find != "":
			if o.Rect != nil {
				return nil, errors.New("cover: give --find or --rect inside the window, not both")
			}
			hits, err := findTextWith(a, FindOptions{Match: *o.Match, Text: o.Find, Source: o.FindSource, Role: o.FindRole, Exact: o.FindExact})
			if err != nil {
				return nil, err
			}
			i := max(o.FindNth, 1) - 1
			if i >= len(hits) {
				return nil, fmt.Errorf("cover: --nth %d but only %d match(es) for %q: %w", o.FindNth, len(hits), o.Find, ErrNoMatch)
			}
			p.found = &hits[i]
			b := hits[i].Bounds
			dx, dy := b.X-w.Bounds.X-o.Pad, b.Y-w.Bounds.Y-o.Pad
			fw, fh := b.W+2*o.Pad, b.H+2*o.Pad
			p.place = func(win Rect) Rect { return Rect{X: win.X + dx, Y: win.Y + dy, W: fw, H: fh} }
		case o.Rect != nil:
			r := *o.Rect
			p.place = func(win Rect) Rect { return r.in(Rect{X: win.X, Y: win.Y, W: win.W, H: win.H}) }
		default:
			p.place = func(win Rect) Rect { return win }
		}
		p.rect = p.place(w.Bounds)
	case o.Find != "":
		return nil, errors.New("cover: --find needs the window to search: add --app or --title")
	case o.Rect != nil || o.Monitor != nil:
		var origin Rect
		if o.Monitor != nil {
			ms, err := listMonitorsWith(a)
			if err != nil {
				return nil, err
			}
			m, err := findMonitor(ms, *o.Monitor)
			if err != nil {
				return nil, err
			}
			origin = monitorRect(m)
		}
		if o.Rect == nil {
			p.rect = origin
		} else {
			if o.Monitor == nil && o.Rect.hasPct() {
				return nil, errors.New("cover: N% in --rect needs a window (--app/--title) or --monitor to be a share of")
			}
			p.rect = o.Rect.in(origin)
		}
	default:
		return nil, errors.New("cover: give a target: --rect=x,y,w,h, --monitor=<n>, or a window (--app/--title, with --rect or --find inside it)")
	}
	if p.rect.W <= 0 || p.rect.H <= 0 {
		return nil, fmt.Errorf("cover: the cover has no area (%dx%d)", p.rect.W, p.rect.H)
	}
	return p, nil
}

func (p *coverPlan) result(a Adapter, hidden bool) CoverResult {
	r := CoverResult{ID: p.opts.ID, Rect: p.rect, Window: p.win, Follow: p.follows(), Hidden: hidden,
		BlockInput: p.opts.BlockInput, Tint: p.look.alpha < 1, Style: p.style, Found: p.found}
	if ms, err := listMonitorsWith(a); err == nil {
		c := frect{float64(p.rect.X), float64(p.rect.Y), float64(p.rect.W), float64(p.rect.H)}.center()
		for _, m := range ms {
			if c.X >= float64(m.X) && c.X < float64(m.X+m.Width) && c.Y >= float64(m.Y) && c.Y < float64(m.Y+m.Height) {
				r.Monitor = m.ID
			}
		}
	}
	return r
}

func (p *coverPlan) follows() bool { return p.win != nil && !p.opts.NoFollow }

// RenderCover renders the cover exactly as it is shown, into a PNG the
// size of the cover, without showing anything.
func RenderCover(o CoverOptions, outPath string) (CoverResult, error) {
	p, err := planCoverWith(defaultAdapter, o)
	if err != nil {
		return CoverResult{}, err
	}
	_, px := defaultAdapter.OverlayScale(p.rect)
	if px <= 0 {
		px = 1
	}
	img := renderCover(p.look, float64(p.rect.W), float64(p.rect.H), px, nil)
	if err := writePNG(outPath, img); err != nil {
		return CoverResult{}, err
	}
	return p.result(defaultAdapter, false), nil
}

// Cover shows a cover and blocks until Duration elapses or Stop fires.
// On macOS it must be called from the main goroutine.
func Cover(o CoverOptions) (CoverResult, error) {
	return coverWith(defaultAdapter, o)
}

// coverTick is how often a cover re-reads its window and its updates.
var coverTick = 16 * time.Millisecond

func coverWith(a Adapter, o CoverOptions) (CoverResult, error) {
	co, ok := a.(core.CoverOverlayer)
	if !ok {
		return CoverResult{}, fmt.Errorf("draw cover: %w", core.ErrNotImplemented)
	}
	p, err := planCoverWith(a, o)
	if err != nil {
		return CoverResult{}, err
	}
	run := &coverRun{a: a, co: co, p: p}
	if err := run.open(); err != nil {
		return CoverResult{}, err
	}
	defer run.win.Close()
	var deadline time.Time
	if o.Duration > 0 {
		deadline = nowFn().Add(o.Duration)
	}
	for {
		if o.Stop != nil {
			select {
			case <-o.Stop:
				return run.res, nil
			default:
			}
		}
		if !deadline.IsZero() && !nowFn().Before(deadline) {
			return run.res, nil
		}
		co.PumpOverlays(coverTick)
		// A failed tick leaves the last frame up: the cover never drops.
		_ = run.tick()
	}
}

// coverRun is a cover on screen.
type coverRun struct {
	a      Adapter
	co     core.CoverOverlayer
	p      *coverPlan
	win    core.CoverWindow
	holes  []frect
	hidden bool
	px     float64
	res    CoverResult
}

func (r *coverRun) scale(rect Rect) float64 {
	_, px := r.a.OverlayScale(rect)
	if px <= 0 {
		px = 1
	}
	return px
}

func (r *coverRun) open() error {
	if r.p.follows() {
		if ws, err := r.a.ListWindows(); err == nil {
			r.holes = occluders(ws, r.p.win.ID, r.p.rect)
		}
	}
	r.px = r.scale(r.p.rect)
	img := renderCover(r.p.look, float64(r.p.rect.W), float64(r.p.rect.H), r.px, r.holes)
	w, err := r.co.OpenCover(r.p.rect, img, r.p.opts.BlockInput)
	if err != nil {
		return err
	}
	r.win = w
	r.publish()
	return nil
}

func (r *coverRun) publish() {
	r.res = r.p.result(r.a, r.hidden)
	if r.p.opts.Moved != nil {
		r.p.opts.Moved(r.res)
	}
}

// tick re-reads the window and any update and shows the change in one
// step. A window list that cannot be read keeps the cover where it is.
func (r *coverRun) tick() error {
	dirty := false
	if r.p.opts.Updates != nil {
		if s := r.p.opts.Updates(); s != nil {
			style := r.p.style.Merge(*s)
			// A bad update keeps the old look: the cover never goes away.
			if l, err := style.look(); err == nil {
				r.p.style, r.p.look = style, l
				dirty = true
			}
			if r.p.opts.UpdateShown != nil {
				defer r.p.opts.UpdateShown()
			}
		}
	}
	moved := false
	if r.p.follows() {
		if ws, err := r.a.ListWindows(); err == nil {
			var cur *Window
			for i := range ws {
				if ws[i].ID == r.p.win.ID {
					cur = &ws[i]
					break
				}
			}
			if cur == nil {
				// Off screen: minimised, hidden, on another Space or closed.
				// Nothing of it shows, so neither does the cover.
				if !r.hidden {
					r.win.SetVisible(false)
					r.hidden = true
					r.publish()
				}
				return r.redraw(dirty, false)
			}
			rect := r.p.place(cur.Bounds)
			holes := occluders(ws, cur.ID, rect)
			if rect != r.p.rect || !sameHoles(holes, r.holes) {
				moved = rect != r.p.rect
				r.p.rect, r.holes = rect, holes
				r.p.win.Bounds = cur.Bounds
				dirty = true
			}
			if r.hidden {
				// Draw it in place first, then show it.
				if err := r.redraw(true, moved); err != nil {
					return err
				}
				r.win.SetVisible(true)
				r.hidden = false
				r.publish()
				return nil
			}
		}
	}
	return r.redraw(dirty, moved)
}

func (r *coverRun) redraw(dirty, moved bool) error {
	if !dirty {
		return nil
	}
	if moved {
		r.px = r.scale(r.p.rect)
	}
	img := renderCover(r.p.look, float64(r.p.rect.W), float64(r.p.rect.H), r.px, r.holes)
	if err := r.win.Update(r.p.rect, img); err != nil {
		return err
	}
	r.publish()
	return nil
}

// occluders are the parts of rect hidden behind windows in front of the
// window id (ws is front-to-back), in rect-local units.
func occluders(ws []Window, id string, rect Rect) []frect {
	box := frect{float64(rect.X), float64(rect.Y), float64(rect.W), float64(rect.H)}
	var out []frect
	for _, w := range ws {
		if w.ID == id {
			return out
		}
		b := w.Bounds
		if in, ok := box.intersect(frect{float64(b.X), float64(b.Y), float64(b.W), float64(b.H)}); ok {
			out = append(out, frect{in.X - box.X, in.Y - box.Y, in.W, in.H})
		}
	}
	// The window is not in the list: leave nothing out.
	return nil
}

func sameHoles(a, b []frect) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CoverPreset returns a named preset: the config's, else a built-in one.
func CoverPreset(name string) (CoverStyle, error) {
	cfg, err := LoadDrawConfig()
	if err != nil {
		return CoverStyle{}, err
	}
	presets := coverPresets(cfg)
	p, ok := presets[name]
	if !ok {
		return CoverStyle{}, fmt.Errorf("cover: no preset %q (have: %s; add more under \"presets\" in %s)", name, presetNames(presets), DrawConfigPath())
	}
	return p, nil
}
