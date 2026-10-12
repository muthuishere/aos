package engine

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/engine/core"
)

// Point is a global (virtual-desktop) coordinate, or monitor-relative
// when DrawOptions.Monitor is set.
type Point struct {
	X int `json:"X"`
	Y int `json:"Y"`
}

// DrawOptions describes one big arrow with a sign, drawn over every
// window: click-through, never takes focus, gone after Duration.
//
// Exactly one target: Point, Region, Match (the matched window's bounds,
// first match wins) or Mouse (the cursor position). Point and Region are
// monitor-relative when Monitor is set, absolute otherwise (the same
// rule as MoveCoords and Screenshot).
type DrawOptions struct {
	Text string

	Point   *Point
	Region  *Rect
	Match   *Match
	Mouse   bool
	Monitor *int

	// Find, with Match, points at text inside that window instead of the
	// whole window: a button, menu item, link or label from the
	// accessibility tree, else OCR'd screen text (see FindText). FindNth
	// picks the n-th
	// best match (1-based; 0 = best). The found element is boxed tightly,
	// as bigarrow does; Style ring circles it instead.
	Find       string
	FindSource FindSource
	FindRole   string
	FindExact  bool
	FindNth    int

	Style   DrawStyle   // arrow (default), ring, box, text
	Shape   DrawShape   // bend (default), straight, zigzag, spiral
	Size    DrawSize    // S, M (default), L, XL
	Border  DrawBorder  // shadow (default), white-black, black
	Corners DrawCorners // round (default), sharp
	From    DrawFrom    // "" / auto, or top-left, top, right, ...

	// Position: for Style text, where the label sits inside its
	// container (bottom-center default; see textPositions) or beside an
	// anchor point; for the other styles an alias of From.
	Position DrawPosition
	// Text-style look and placement. Margin is px or "N%" (default the
	// subtitle safe area: 8% top/bottom, 5% sides); MaxWidth px or "N%"
	// of the container (default 80%); Background a colour, "none" or
	// "auto" (default: translucent contrast of Color); Align left /
	// center / right; Weight regular / bold; FontSize an explicit text
	// size in units (overrides Size); MaxLines before shrinking (2).
	Margin     string
	Offset     *Point
	Background string
	Align      string
	MaxWidth   string
	Weight     string
	FontSize   float64
	MaxLines   int
	// Subtitle uses the "subtitle" config defaults and sizes the font
	// from the container height (film-subtitle look).
	Subtitle bool

	Color       *Color // default red
	TextColor   *Color // default: readable contrast of Color
	BorderColor *Color
	EdgeColor   *Color

	// Font is a font file path or system font name; FontScale multiplies
	// the S/M/L font sizes. Empty/0 falls back to draw.json, then
	// to a heavy rounded system font, then to the embedded Go Bold.
	Font      string
	FontScale float64
	// NoConfig skips the config file (see DrawConfigPath).
	NoConfig bool
	// Animate: on (default) draws the drawing in (stroke reveal, sign
	// fade-in), pulses softly at the target while shown and fades out;
	// off shows and removes it at once. The OS reduce-motion setting
	// keeps only plain fades.
	Animate DrawAnimate

	// Duration bounds how long Draw shows the drawing; 0 means until
	// Stop fires (or forever when Stop is nil too).
	Duration time.Duration
	// Stop, when non-nil, ends Draw early once it is closed or sent to.
	Stop <-chan struct{}

	// Avoid lists the global sign rects of other live drawings; the sign
	// is placed clear of them while possible.
	Avoid []Rect
}

// DrawResult says where the drawing landed, in global coordinates, so a
// caller can verify it against a screenshot before showing the human.
type DrawResult struct {
	Monitor int    `json:"Monitor"`
	Sign    Rect   `json:"Sign"`
	Tip     Point  `json:"Tip"`
	From    string `json:"From"`
	// Surface is the drawn area (the monitor's bounds); an image from
	// RenderDraw maps onto it pixel-for-unit scaled by PixelScale.
	Surface    Rect    `json:"Surface"`
	PixelScale float64 `json:"PixelScale"`
	// Found is what --find matched, when it was used.
	Found *FoundText `json:"Found,omitempty"`
}

// Draw shows the drawing and blocks until Duration elapses or Stop
// fires. Needs no admin rights and no macOS permission. On macOS it
// must be called from the main goroutine (AppKit owns the main thread).
func Draw(opts DrawOptions) (DrawResult, error) {
	return drawWith(defaultAdapter, opts)
}

// RenderDraw renders the drawing exactly as Draw would show it into a
// transparent PNG the size of the monitor, without showing anything.
func RenderDraw(opts DrawOptions, outPath string) (DrawResult, error) {
	if outPath == "" {
		return DrawResult{}, errors.New("draw: output path is required")
	}
	img, res, err := renderDrawWith(defaultAdapter, opts)
	if err != nil {
		return res, err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return res, fmt.Errorf("draw: %w", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		return res, fmt.Errorf("draw: encode png: %w", err)
	}
	return res, nil
}

func drawWith(a Adapter, opts DrawOptions) (DrawResult, error) {
	sc, err := planDrawWith(a, opts)
	if err != nil {
		return DrawResult{}, err
	}
	img, res := sc.render(), sc.res
	var deadline time.Time
	if opts.Duration > 0 {
		deadline = nowFn().Add(opts.Duration)
	}
	done := func() bool {
		if opts.Stop != nil {
			select {
			case <-opts.Stop:
				return true
			default:
			}
		}
		return !deadline.IsZero() && !nowFn().Before(deadline)
	}
	if anim, ok := a.(core.OverlayAnimator); ok && sc.opts.Animate != AnimateOff {
		return res, animateDraw(anim, sc, img, done)
	}
	return res, a.ShowOverlay(res.Surface, img, done)
}

// resolveDrawTarget turns the options into a global target (point or
// rect) and the monitor it is drawn on.
func resolveDrawTarget(a Adapter, o DrawOptions) (frect, bool, Monitor, error) {
	n := 0
	for _, set := range []bool{o.Point != nil, o.Region != nil, o.Match != nil, o.Mouse} {
		if set {
			n++
		}
	}
	if n != 1 {
		return frect{}, false, Monitor{}, errors.New("draw: give exactly one target: --at X,Y, --rect X,Y,W,H, --title/--app, or --mouse")
	}
	ms, err := listMonitorsWith(a)
	if err != nil {
		return frect{}, false, Monitor{}, err
	}
	var origin Point
	if o.Monitor != nil {
		m, err := findMonitor(ms, *o.Monitor)
		if err != nil {
			return frect{}, false, Monitor{}, err
		}
		origin = Point{m.X, m.Y}
	}
	var t frect
	isPoint := false
	switch {
	case o.Point != nil:
		t, isPoint = frect{float64(o.Point.X + origin.X), float64(o.Point.Y + origin.Y), 0, 0}, true
	case o.Mouse:
		x, y, err := a.CursorPosition()
		if err != nil {
			return frect{}, false, Monitor{}, err
		}
		t, isPoint = frect{float64(x), float64(y), 0, 0}, true
	case o.Region != nil:
		r := *o.Region
		if r.W <= 0 || r.H <= 0 {
			return frect{}, false, Monitor{}, fmt.Errorf("draw: --rect needs a positive width and height, got %dx%d", r.W, r.H)
		}
		t = frect{float64(r.X + origin.X), float64(r.Y + origin.Y), float64(r.W), float64(r.H)}
	case o.Match != nil:
		w, err := matchOne(a, *o.Match)
		if err != nil {
			return frect{}, false, Monitor{}, err
		}
		b := w.Bounds
		t = frect{float64(b.X), float64(b.Y), float64(b.W), float64(b.H)}
	}
	c := t.center()
	if isPoint {
		c = vec{t.X, t.Y}
	}
	for _, m := range ms {
		if c.X >= float64(m.X) && c.X < float64(m.X+m.Width) && c.Y >= float64(m.Y) && c.Y < float64(m.Y+m.Height) {
			return t, isPoint, m, nil
		}
	}
	if o.Match != nil {
		w := Window{Bounds: Rect{X: int(t.X), Y: int(t.Y), W: int(t.W), H: int(t.H)}}
		m, err := resolveCurrentMonitor(w, ms)
		return t, false, m, err
	}
	return frect{}, false, Monitor{}, fmt.Errorf("draw: target %.0f,%.0f is not on any monitor (see `windowctl monitors list`)", c.X, c.Y)
}

func renderDrawWith(a Adapter, opts DrawOptions) (*image.RGBA, DrawResult, error) {
	sc, err := planDrawWith(a, opts)
	if err != nil {
		return nil, DrawResult{}, err
	}
	return sc.render(), sc.res, nil
}

// drawScene is a placed drawing, ready to render in any pose.
type drawScene struct {
	opts    DrawOptions // validated, config applied
	text    *textScene  // set for Style text: a label, no arrow
	lay     drawLayout
	st      signText
	look    drawLook
	px      float64
	surface image.Rectangle // the whole monitor, in image pixels
	res     DrawResult
}

// render paints the finished drawing over the whole surface.
func (sc *drawScene) render() *image.RGBA {
	cv := newCanvasIn(sc.surface, sc.px)
	if sc.text != nil {
		renderTextPose(cv, sc.text, finalPose)
		return cv.img
	}
	renderLayout(cv, sc.lay, sc.st, sc.look)
	return cv.img
}

func planDrawWith(a Adapter, opts DrawOptions) (*drawScene, error) {
	opts.Style = DrawStyle(strings.ToLower(string(opts.Style)))
	if !opts.NoConfig {
		cfg, err := LoadDrawConfig()
		if err != nil {
			return nil, err
		}
		if err := opts.applyConfig(cfg); err != nil {
			return nil, err
		}
	}
	sizeGiven := opts.Size != ""
	if err := opts.validate(); err != nil {
		return nil, err
	}
	if opts.Text == "" {
		return nil, errors.New("draw: --text is required: the sign says what to do")
	}
	if opts.Style == DrawText {
		return planTextScene(a, opts, sizeGiven)
	}
	var found *FoundText
	if opts.Find != "" {
		if opts.Match == nil {
			return nil, errors.New("draw: --find needs the window to search: add --title or --app")
		}
		hits, err := findTextWith(a, FindOptions{
			Match: *opts.Match, Text: opts.Find, Source: opts.FindSource, Role: opts.FindRole, Exact: opts.FindExact,
		})
		if err != nil {
			return nil, err
		}
		i := max(opts.FindNth, 1) - 1
		if i >= len(hits) {
			return nil, fmt.Errorf("draw: --nth %d but only %d match(es) for %q", opts.FindNth, len(hits), opts.Find)
		}
		found = &hits[i]
		r := found.Bounds
		opts.Match, opts.Monitor, opts.Region = nil, nil, &r
	}
	target, isPoint, mon, err := resolveDrawTarget(a, opts)
	if err != nil {
		return nil, err
	}
	surface := Rect{X: mon.X, Y: mon.Y, W: mon.Width, H: mon.Height}
	ui, px := a.OverlayScale(surface)
	if ui <= 0 {
		ui = 1
	}
	if px <= 0 {
		px = 1
	}
	// Work in surface-local units from here on.
	local := frect{target.X - float64(mon.X), target.Y - float64(mon.Y), target.W, target.H}
	bounds := frect{0, 0, float64(mon.Width), float64(mon.Height)}
	face, err := loadSignFont(opts.Font)
	if err != nil {
		return nil, err
	}
	m := opts.Size.metrics().scaled(ui)
	if opts.FontScale > 0 {
		m.fontSize *= opts.FontScale
	}
	maxText := bounds.W*drawMaxSignShare - signPadX*2*ui
	st := fitText(face, opts.Text, m, ui, px, maxText)
	signW := math.Ceil(st.w/px + signPadX*2*ui)
	signH := math.Ceil(st.h/px + signPadY*2*ui)
	var others []frect
	for _, r := range opts.Avoid {
		others = append(others, frect{float64(r.X - mon.X), float64(r.Y - mon.Y), float64(r.W), float64(r.H)})
	}
	lay := planLayout(local, isPoint, bounds, bounds, signW, signH, opts, ui, others)
	res := DrawResult{
		Monitor: mon.ID,
		Sign: Rect{X: mon.X + int(math.Round(lay.sign.X)), Y: mon.Y + int(math.Round(lay.sign.Y)),
			W: int(math.Round(lay.sign.W)), H: int(math.Round(lay.sign.H))},
		Tip:        Point{mon.X + int(math.Round(lay.arrow.tip.X)), mon.Y + int(math.Round(lay.arrow.tip.Y))},
		From:       string(lay.from),
		Surface:    surface,
		PixelScale: px,
		Found:      found,
	}
	return &drawScene{
		opts: opts, lay: lay, st: st, look: lookOf(opts), px: px, res: res,
		surface: image.Rect(0, 0, int(math.Ceil(bounds.W*px)), int(math.Ceil(bounds.H*px))),
	}, nil
}
