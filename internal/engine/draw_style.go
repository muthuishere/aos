package engine

// The look vocabulary of `windowctl draw`, ported from bigarrow
// (github.com/franzenzenhofer/big-arrow-on-the-screen, MIT): the same
// flag names, presets and size metrics, so an agent that knows one
// knows the other. Everything here is OS-agnostic.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// DrawStyle is how the target is marked: an arrow alone, or an outline
// ring/box around the target plus the arrow. Outlines never fill — the
// human must still see what is being pointed at.
type DrawStyle string

const (
	DrawArrow DrawStyle = "arrow"
	DrawRing  DrawStyle = "ring"
	DrawBox   DrawStyle = "box"
)

// DrawShape is the shaft between sign and head.
type DrawShape string

const (
	ShapeBend     DrawShape = "bend"
	ShapeStraight DrawShape = "straight"
	ShapeZigzag   DrawShape = "zigzag"
	ShapeSpiral   DrawShape = "spiral"
)

// DrawSize selects one of the S/M/L metric presets.
type DrawSize string

const (
	SizeS DrawSize = "S"
	SizeM DrawSize = "M"
	SizeL DrawSize = "L"
)

// DrawBorder is how arrow and sign stand off the screen behind them:
// contrast border + drop shadow (default), contrast border + thin black
// edge, or only a thin black outline.
type DrawAnimate string

const (
	AnimateOn  DrawAnimate = "on"
	AnimateOff DrawAnimate = "off"
)

type DrawBorder string

const (
	BorderShadow     DrawBorder = "shadow"
	BorderWhiteBlack DrawBorder = "white-black"
	BorderBlack      DrawBorder = "black"
)

// DrawCorners is the sign's corner style.
type DrawCorners string

const (
	CornersRound DrawCorners = "round"
	CornersSharp DrawCorners = "sharp"
)

// DrawFrom is the side of the target the sign sits on; the arrow
// approaches from there. Empty means auto (most free space).
type DrawFrom string

var drawFromUnits = map[DrawFrom]vec{
	"top-left":     vec{-1, -1}.norm(),
	"top-right":    vec{1, -1}.norm(),
	"bottom-left":  vec{-1, 1}.norm(),
	"bottom-right": vec{1, 1}.norm(),
	"left":         {-1, 0},
	"right":        {1, 0},
	"top":          {0, -1},
	"bottom":       {0, 1},
}

// drawFromOrder is the deterministic order placement tries sides in.
var drawFromOrder = []DrawFrom{"top-left", "top-right", "bottom-left", "bottom-right", "left", "right", "top", "bottom"}

// Color is an sRGB colour in 0..1.
type Color struct{ R, G, B float64 }

// colorPresets are tuned so the sign text passes WCAG AA (≥ 4.5:1)
// against the fill where the hue allows, and each colour still pops
// over both light and dark UIs. red is the default (the annotation-red
// of Markup/Skitch/CleanShot, deepened from bigarrow's FF3B1F, whose
// white text only reaches 3.6:1).
var colorPresets = []struct{ name, hex string }{
	{"red", "E5261A"}, {"orange", "D93A00"}, {"amber", "FF8A00"}, {"yellow", "FFD60A"},
	{"green", "1A7F37"}, {"teal", "00B5AD"}, {"blue", "0A6CFF"}, {"purple", "8E44FF"},
	{"pink", "D81B60"}, {"black", "1C1C1E"}, {"white", "FFFFFF"},
}

// ParseColor accepts a preset name or a hex colour (#RRGGBB / #RGB,
// with or without '#').
func ParseColor(raw string) (Color, error) {
	key := strings.ToLower(strings.TrimSpace(raw))
	for _, p := range colorPresets {
		if p.name == key {
			key = p.hex
		}
	}
	digits := strings.TrimPrefix(key, "#")
	if len(digits) == 3 {
		digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
	}
	v, err := strconv.ParseUint(digits, 16, 32)
	if len(digits) != 6 || err != nil {
		names := make([]string, len(colorPresets))
		for i, p := range colorPresets {
			names[i] = p.name
		}
		return Color{}, fmt.Errorf("colour %q is not valid, use a preset (%s) or hex like #FF3B1F", raw, strings.Join(names, ", "))
	}
	return Color{float64(v>>16&0xFF) / 255, float64(v>>8&0xFF) / 255, float64(v&0xFF) / 255}, nil
}

func (c Color) luminance() float64 { return 0.2126*c.R + 0.7152*c.G + 0.0722*c.B }

// relLum is WCAG relative luminance (linearized sRGB).
func (c Color) relLum() float64 {
	lin := func(v float64) float64 {
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrastRatio(a, b Color) float64 {
	la, lb := a.relLum()+0.05, b.relLum()+0.05
	return math.Max(la, lb) / math.Min(la, lb)
}

var (
	inkColor   = Color{0x1C / 255.0, 0x1C / 255.0, 0x1E / 255.0}
	whiteColor = Color{1, 1, 1}
)

// contrast is the sign text / border colour: whichever of white and ink
// has the higher WCAG contrast against c.
func (c Color) contrast() Color {
	if contrastRatio(c, whiteColor) >= contrastRatio(c, inkColor) {
		return whiteColor
	}
	return inkColor
}

// outline is the thin line of --border black: black, white on near-black arrows.
func (c Color) outline() Color {
	if c.luminance() < 0.2 {
		return Color{1, 1, 1}
	}
	return Color{}
}

// drawMetrics are the size-dependent dimensions, in layout units
// (points on macOS, scaled by the monitor's DPI factor on Windows).
type drawMetrics struct {
	stroke, headLength, headWidth, fontSize, reach float64
}

func (s DrawSize) metrics() drawMetrics {
	switch s {
	case SizeS:
		return drawMetrics{10, 46, 48, 30, 130}
	case SizeL:
		return drawMetrics{21, 84, 88, 60, 220}
	case SizeXL:
		return drawMetrics{27, 106, 110, 78, 270}
	default:
		return drawMetrics{15, 62, 64, 44, 170}
	}
}

func (m drawMetrics) scaled(k float64) drawMetrics {
	return drawMetrics{m.stroke * k, m.headLength * k, m.headWidth * k, m.fontSize * k, m.reach * k}
}

const (
	drawMinFontSize   = 24
	drawMaxLines      = 3
	drawMaxSignShare  = 0.6
	drawOutlineWidth  = 6  // contrast border added to every stroke
	drawEdgeWidth     = 3  // thin black edge added on top of that
	drawMarkLineWidth = 6  // ring / box line
	drawShadowRadius  = 10 // drop-shadow blur radius
	drawShadowDrop    = 3  // drop-shadow offset, straight down
	drawShadowOpacity = 0.35
)

// validate normalizes enum fields, filling defaults and rejecting
// unknown values with an actionable message.
func (o *DrawOptions) validate() error {
	pick := func(field, val string, def string, allowed ...string) (string, error) {
		if val == "" {
			return def, nil
		}
		v := strings.ToLower(val)
		for _, a := range allowed {
			if strings.ToLower(a) == v {
				return a, nil
			}
		}
		return "", fmt.Errorf("draw: %s %q is not one of %s", field, val, strings.Join(allowed, ", "))
	}
	var err error
	var s string
	if s, err = pick("style", string(o.Style), "arrow", "arrow", "ring", "box", "text"); err != nil {
		return err
	}
	o.Style = DrawStyle(s)
	if s, err = pick("shape", string(o.Shape), "bend", "bend", "straight", "zigzag", "spiral"); err != nil {
		return err
	}
	o.Shape = DrawShape(s)
	if s, err = pick("size", string(o.Size), "M", "S", "M", "L", "XL"); err != nil {
		return err
	}
	o.Size = DrawSize(s)
	if s, err = pick("border", string(o.Border), "shadow", "shadow", "white-black", "black"); err != nil {
		return err
	}
	o.Border = DrawBorder(s)
	if s, err = pick("corners", string(o.Corners), "round", "round", "sharp"); err != nil {
		return err
	}
	o.Corners = DrawCorners(s)
	if s, err = pick("animate", string(o.Animate), "on", "on", "off"); err != nil {
		return err
	}
	o.Animate = DrawAnimate(s)
	// --position is the sign's side for arrow / ring / box (alias of From).
	if o.Style != DrawText && o.Position != "" && o.From == "" {
		switch p := DrawFrom(strings.ToLower(string(o.Position))); p {
		case "center":
			return fmt.Errorf("draw: position center only applies to --style text; for an arrow give the sign's side (top-left, top, right, ...)")
		case "top-center":
			o.From = "top"
		case "bottom-center":
			o.From = "bottom"
		default:
			o.From = p
		}
	}
	if o.From == "auto" {
		o.From = ""
	}
	if o.From != "" {
		if _, ok := drawFromUnits[DrawFrom(strings.ToLower(string(o.From)))]; !ok {
			names := []string{"auto"}
			for _, d := range drawFromOrder {
				names = append(names, string(d))
			}
			return fmt.Errorf("draw: from %q is not one of %s", o.From, strings.Join(names, ", "))
		}
		o.From = DrawFrom(strings.ToLower(string(o.From)))
	}
	if o.Color == nil && o.Style != DrawText {
		c, _ := ParseColor("red")
		o.Color = &c
	}
	return nil
}
