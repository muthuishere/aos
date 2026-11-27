package engine

import (
	"fmt"
	"image"
	"math"
	"strings"
)

// DrawBubble is the speech-bubble style: a rounded card with a tail that
// points at the target (a window, found text, a point, a rect).
const DrawBubble DrawStyle = "bubble"

// bubbleSides is the order a bubble tries to sit around its target.
var bubbleSides = []string{"top", "bottom", "right", "left"}

// bubbleSide maps a --from / --position value to the side the bubble sits on.
func bubbleSide(from DrawFrom) string {
	switch s := string(from); {
	case strings.HasPrefix(s, "top"):
		return "top"
	case strings.HasPrefix(s, "bottom"):
		return "bottom"
	case s == "left" || s == "right":
		return s
	}
	return ""
}

// planBubbleScene lays a bubble beside the target, tail pointing at it.
func planBubbleScene(a Adapter, opts DrawOptions, found *FoundText) (*drawScene, error) {
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
	bounds := frect{0, 0, float64(mon.Width), float64(mon.Height)}
	t := frect{target.X - float64(mon.X), target.Y - float64(mon.Y), target.W, target.H}

	size := textFontSize(opts.Size) * ui
	if opts.FontScale > 0 {
		size *= opts.FontScale
	}
	weight := opts.Weight
	if weight == "" {
		weight = "bold"
	}
	fnt, err := loadTextFont(opts.Font, weight)
	if err != nil {
		return nil, err
	}
	padX, padY := 0.75*size, 0.5*size
	maxW := math.Min(bounds.W*0.4, 380*ui)
	if opts.MaxWidth != "" {
		v, err := parseLength(opts.MaxWidth, bounds.W)
		if err != nil {
			return nil, fmt.Errorf("draw: --max-width %w", err)
		}
		if !strings.HasSuffix(strings.TrimSpace(opts.MaxWidth), "%") {
			v *= ui
		}
		maxW = math.Max(80*ui, math.Min(v, bounds.W-16*ui))
	}
	st := layoutText(fnt, opts.Text, size*px, (maxW-2*padX)*px)
	w := math.Ceil(st.w/px + 2*padX)
	h := math.Ceil(st.h/px + 2*padY)

	bg := whiteColor
	if opts.Color != nil {
		bg = *opts.Color
	}
	fg := bg.contrast()
	if opts.TextColor != nil {
		fg = *opts.TextColor
	}
	radius := math.Min(h/2, 0.6*size)
	tailLen, tailW := 0.9*size+8*ui, 0.9*size
	edge := 8 * ui

	// Where the tail touches the target: the facing edge's middle, or the point.
	tipFor := func(side string) vec {
		c := t.center()
		if isPoint {
			return vec{t.X, t.Y}
		}
		switch side {
		case "top":
			return vec{c.X, t.Y}
		case "bottom":
			return vec{c.X, t.Y + t.H}
		case "left":
			return vec{t.X, c.Y}
		}
		return vec{t.X + t.W, c.Y}
	}
	boxFor := func(side string, tip vec) frect {
		var b frect
		b.W, b.H = w, h
		switch side {
		case "top":
			b.X, b.Y = tip.X-w/2, tip.Y-tailLen-h
		case "bottom":
			b.X, b.Y = tip.X-w/2, tip.Y+tailLen
		case "left":
			b.X, b.Y = tip.X-tailLen-w, tip.Y-h/2
		default:
			b.X, b.Y = tip.X+tailLen, tip.Y-h/2
		}
		return b
	}
	fits := func(b frect) bool {
		return b.X >= 0 && b.Y >= 0 && b.X+b.W <= bounds.W && b.Y+b.H <= bounds.H
	}
	order := bubbleSides
	if pref := bubbleSide(opts.From); pref != "" {
		order = append([]string{pref}, bubbleSides...)
	}
	side, tip, box := order[0], tipFor(order[0]), frect{}
	box = boxFor(side, tip)
	for _, s := range order {
		tp := tipFor(s)
		if b := boxFor(s, tp); fits(b) {
			side, tip, box = s, tp, b
			break
		}
	}
	// Keep the card on the screen; the tail still reaches the target.
	box.X = math.Max(edge, math.Min(box.X, bounds.W-box.W-edge))
	box.Y = math.Max(edge, math.Min(box.Y, bounds.H-box.H-edge))
	box.X, box.Y = math.Round(box.X), math.Round(box.Y)

	// Tail: a triangle from the card edge facing the target to the tip.
	clamp := func(v, lo, hi float64) float64 {
		if hi < lo {
			return (lo + hi) / 2
		}
		return math.Max(lo, math.Min(v, hi))
	}
	var tail []vec
	const overlap = 1.0 // sink the base into the card so no seam shows
	switch side {
	case "top":
		bx := clamp(tip.X, box.X+radius+tailW/2, box.X+box.W-radius-tailW/2)
		tail = []vec{{bx - tailW/2, box.Y + box.H - overlap}, {bx + tailW/2, box.Y + box.H - overlap}, tip}
	case "bottom":
		bx := clamp(tip.X, box.X+radius+tailW/2, box.X+box.W-radius-tailW/2)
		tail = []vec{{bx - tailW/2, box.Y + overlap}, {bx + tailW/2, box.Y + overlap}, tip}
	case "left":
		by := clamp(tip.Y, box.Y+radius+tailW/2, box.Y+box.H-radius-tailW/2)
		tail = []vec{{box.X + box.W - overlap, by - tailW/2}, {box.X + box.W - overlap, by + tailW/2}, tip}
	default:
		by := clamp(tip.Y, box.Y+radius+tailW/2, box.Y+box.H-radius-tailW/2)
		tail = []vec{{box.X + overlap, by - tailW/2}, {box.X + overlap, by + tailW/2}, tip}
	}

	ts := &textScene{
		box: box, content: box.inset(padX, padY), st: st, align: "center",
		fg: fg, bg: &bg, bgAlpha: 1, radius: radius, shadow: true, ui: ui, tail: tail,
	}
	res := DrawResult{
		Monitor:    mon.ID,
		Sign:       Rect{X: mon.X + int(box.X), Y: mon.Y + int(box.Y), W: int(box.W), H: int(box.H)},
		Tip:        Point{mon.X + int(math.Round(tip.X)), mon.Y + int(math.Round(tip.Y))},
		From:       side,
		Surface:    surface,
		PixelScale: px,
		Found:      found,
	}
	return &drawScene{
		opts: opts, text: ts, px: px, res: res,
		lay:     drawLayout{sign: box, uiScale: ui, anchor: tip},
		surface: image.Rect(0, 0, int(math.Ceil(bounds.W*px)), int(math.Ceil(bounds.H*px))),
	}, nil
}
