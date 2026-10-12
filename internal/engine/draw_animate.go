package engine

// Animation for shown drawings: a short entrance (the stroke draws
// itself, the sign fades and slides in, a ring / box settles from 92%),
// a soft attention pulse at the target every couple of seconds, and a
// fade-out. Frames are rendered here in pure Go, cropped to the drawing,
// so they are identical on every OS and testable without a screen; the
// adapters only swap pictures and opacity on their overlay windows.
//
// CPU: frames at ~60fps only during the entrance and the fade-out; the
// pulse lives in its own small window (the main shape is never redrawn,
// so it cannot flicker) with pre-rendered frames at 30fps; otherwise the
// loop just waits.

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/muthuishere/aos/internal/engine/core"
)

// Timings (vars so tests can shorten them).
var (
	animEntrance    = 450 * time.Millisecond
	animSignIn      = 280 * time.Millisecond // sign fade + slide, from 0
	animStrokeDelay = 80 * time.Millisecond  // stroke starts just after the sign
	animExit        = 200 * time.Millisecond
	animPlainFadeIn = 200 * time.Millisecond // reduce-motion entrance
	animPulseFirst  = 250 * time.Millisecond // after the entrance
	animPulsePeriod = 2 * time.Second
	animPulseLength = 900 * time.Millisecond
	animFrame       = time.Second / 60
	animPulseFrame  = time.Second / 30
	animIdle        = 100 * time.Millisecond // done() polling while still
)

const (
	animSignSlide  = 7.0  // sign slides in from this far below (units, x ui)
	animMarkFrom   = 0.92 // ring / box start scale
	animPulseGrow  = 0.5  // pulse ring grows from 1.0 to 1.5 x its radius
	animPulseAlpha = 0.5  // pulse ring start opacity
	animPulseLine  = 3.0  // pulse ring line width (x ui)
	animCropPad    = 14.0 // crop margin around the drawing (units, x ui)
)

// drawPose is one moment of the entrance.
type drawPose struct {
	reveal    float64 // 0..1 of the arrow / mark stroke drawn
	markScale float64 // ring / box scale about its centre
	signAlpha float64
	signDY    float64 // sign offset in units, down
}

var finalPose = drawPose{reveal: 1, markScale: 1, signAlpha: 1}

func (p drawPose) signSettled() bool { return p.signAlpha >= 1 && p.signDY == 0 }

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func easeOutCubic(u float64) float64 { u = 1 - clamp01(u); return 1 - u*u*u }

// entrancePose is the pose t into the entrance; t >= animEntrance is
// finalPose exactly.
func entrancePose(t time.Duration, ui float64) drawPose {
	if t >= animEntrance {
		return finalPose
	}
	sig := easeOutCubic(float64(t) / float64(animSignIn))
	str := easeOutCubic(float64(t-animStrokeDelay) / float64(animEntrance-animStrokeDelay))
	return drawPose{
		reveal: str, markScale: animMarkFrom + (1-animMarkFrom)*str,
		signAlpha: sig, signDY: animSignSlide * ui * (1 - sig),
	}
}

// polylineLength is the arc length of pts.
func polylineLength(pts []vec) float64 {
	l := 0.0
	for i := 1; i < len(pts); i++ {
		l += pts[i-1].dist(pts[i])
	}
	return l
}

// truncatePolyline keeps the first `length` units of pts.
func truncatePolyline(pts []vec, length float64) []vec {
	if len(pts) == 0 || length <= 0 {
		return nil
	}
	out := []vec{pts[0]}
	for i := 1; i < len(pts); i++ {
		d := pts[i-1].dist(pts[i])
		if d >= length {
			if d > 0 {
				out = append(out, pts[i-1].add(pts[i].sub(pts[i-1]).mul(length/d)))
			}
			return out
		}
		length -= d
		out = append(out, pts[i])
	}
	return out
}

func scaleAbout(pts []vec, c vec, k float64) []vec {
	out := make([]vec, len(pts))
	for i, p := range pts {
		out[i] = c.add(p.sub(c).mul(k))
	}
	return out
}

// arrow is g drawn up to the pose's reveal: the shaft grows from the
// sign, then the head grows out of the shaft's end.
func (p drawPose) arrow(g arrowGeom) arrowGeom {
	if p.reveal >= 1 {
		return g
	}
	if p.reveal <= 0 || len(g.shaft) == 0 {
		return arrowGeom{tip: g.tip}
	}
	end := g.shaft[len(g.shaft)-1]
	shaftLen, headLen := polylineLength(g.shaft), end.dist(g.tip)
	drawn := p.reveal * (shaftLen + headLen)
	out := arrowGeom{tip: g.tip, root: g.root, shaft: truncatePolyline(g.shaft, drawn)}
	if headLen > 0 && drawn > shaftLen {
		out.head = scaleAbout(g.head, end, clamp01((drawn-shaftLen)/headLen))
	}
	return out
}

// markPath is the ring / box outline drawn up to the pose.
func (p drawPose) markPath(path []vec, m drawMark) []vec {
	if len(path) == 0 || (p.reveal >= 1 && p.markScale == 1) {
		return path
	}
	c := m.center
	if m.kind == markBox {
		c = m.rect.center()
	}
	path = scaleAbout(path, c, p.markScale)
	return truncatePolyline(path, p.reveal*polylineLength(path))
}

// ---- frames -----------------------------------------------------------

// animCrop is the part of the surface the animated window covers: the
// drawing's pixels plus room for the sign's slide, aligned to whole
// units so the window sits exactly where the full-surface image would.
func (sc *drawScene) animCrop(final *image.RGBA) (image.Rectangle, Rect) {
	used := opaqueBounds(final)
	if used.Empty() {
		used = image.Rect(0, 0, 1, 1)
	}
	pad := animCropPad * sc.lay.uiScale
	ux0 := math.Max(0, math.Floor(float64(used.Min.X)/sc.px-pad))
	uy0 := math.Max(0, math.Floor(float64(used.Min.Y)/sc.px-pad))
	ux1 := math.Min(float64(sc.surface.Max.X)/sc.px, math.Ceil(float64(used.Max.X)/sc.px+pad))
	uy1 := math.Min(float64(sc.surface.Max.Y)/sc.px, math.Ceil(float64(used.Max.Y)/sc.px+pad))
	pr := image.Rect(int(math.Round(ux0*sc.px)), int(math.Round(uy0*sc.px)),
		int(math.Round(ux1*sc.px)), int(math.Round(uy1*sc.px))).Intersect(sc.surface)
	s := sc.res.Surface
	return pr, Rect{X: s.X + int(ux0), Y: s.Y + int(uy0), W: int(ux1 - ux0), H: int(uy1 - uy0)}
}

// frame renders the drawing in pose, cropped to r.
func (sc *drawScene) frame(r image.Rectangle, pose drawPose) *image.RGBA {
	cv := newCanvasIn(r, sc.px)
	if sc.text != nil {
		renderTextPose(cv, sc.text, pose)
		return cv.img
	}
	renderLayoutPose(cv, sc.lay, sc.st, sc.look, pose)
	return cv.img
}

// cropCopy copies r out of img into a tightly packed image.
func cropCopy(img *image.RGBA, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(r)
	draw.Draw(out, r, img, r.Min, draw.Src)
	return out
}

// pulseSpec is where the attention pulse rings out from: the target
// point itself for point targets, else the arrow tip.
func (sc *drawScene) pulseSpec() (center vec, r0 float64) {
	ui := sc.lay.uiScale
	if m := sc.lay.mark; m.kind == markRing && sc.lay.pointTarget {
		return m.center, math.Max(m.rx, m.ry)
	}
	if sc.lay.pointTarget {
		return sc.lay.anchor, 16 * ui
	}
	return sc.lay.arrow.tip, 16 * ui
}

// pulseWindow is the pulse's own small window (units, surface-local)
// and its frames at 30fps; frame opacity rides on SetOpacity.
type pulseAnim struct {
	unit   frect
	frames []*image.RGBA
}

func (sc *drawScene) pulseFrames() pulseAnim {
	ui := sc.lay.uiScale
	c, r0 := sc.pulseSpec()
	hs := math.Ceil(r0*(1+animPulseGrow) + animPulseLine*ui + 3*ui)
	u := frect{math.Floor(c.X - hs), math.Floor(c.Y - hs), 2 * hs, 2 * hs}
	n := int(math.Round(u.W * sc.px))
	count := max(1, int(animPulseLength/animPulseFrame))
	pa := pulseAnim{unit: u}
	for i := 0; i < count; i++ {
		p := easeOutCubic(float64(i) / float64(count))
		cv := newCanvas(n, n, sc.px)
		r := r0 * (1 + animPulseGrow*p)
		ring := ellipse(c.sub(vec{u.X, u.Y}), r, r)
		cv.stroke(ring, (animPulseLine+2)*ui, sc.look.border)
		cv.stroke(ring, animPulseLine*ui, sc.look.color)
		pa.frames = append(pa.frames, cv.img)
	}
	return pa
}

// pulseAt says which pulse frame shows t after the entrance ended, at
// what opacity; ok is false between pulses.
func pulseAt(t time.Duration, frames int) (idx int, alpha float64, ok bool) {
	t -= animPulseFirst
	if t < 0 {
		return 0, 0, false
	}
	t %= animPulsePeriod
	if t >= animPulseLength {
		return 0, 0, false
	}
	u := float64(t) / float64(animPulseLength)
	return min(frames-1, int(u*float64(frames))), animPulseAlpha * (1 - easeOutCubic(u)), true
}

// untilNextPulse is how long after t the next pulse begins.
func untilNextPulse(t time.Duration) time.Duration {
	t -= animPulseFirst
	if t < 0 {
		return -t
	}
	return animPulsePeriod - t%animPulsePeriod
}

// ---- the show loop -----------------------------------------------------

// animateDraw shows the scene with the entrance, pulse and exit until
// done() reports true, then fades out.
func animateDraw(a core.OverlayAnimator, sc *drawScene, final *image.RGBA, done func() bool) error {
	// Windows belong to the thread that made them (and on macOS this is
	// the already-locked main thread).
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	pr, bounds := sc.animCrop(final)
	finalCrop := cropCopy(final, pr)
	reduce := a.ReduceMotion()
	clock := nowFn

	var first *image.RGBA
	opacity := 1.0
	if reduce {
		first, opacity = finalCrop, 0
	} else {
		first = sc.frame(pr, entrancePose(0, sc.lay.uiScale))
	}
	main, err := a.OpenOverlay(bounds, first, opacity)
	if err != nil {
		return err
	}
	defer main.Close()
	var pulse core.Overlay
	defer func() {
		if pulse != nil {
			pulse.Close()
		}
	}()

	// Entrance.
	start := clock()
	for !done() {
		t := clock().Sub(start)
		if reduce {
			main.SetOpacity(clamp01(float64(t) / float64(animPlainFadeIn)))
			if t >= animPlainFadeIn {
				break
			}
		} else {
			if t >= animEntrance {
				if err := main.SetImage(finalCrop); err != nil {
					return err
				}
				break
			}
			if err := main.SetImage(sc.frame(pr, entrancePose(t, sc.lay.uiScale))); err != nil {
				return err
			}
		}
		a.PumpOverlays(animFrame)
	}

	// Shown: the pulse, or nothing at all.
	var pa pulseAnim
	shown := clock()
	for !done() {
		if reduce || sc.text != nil { // text drawings never pulse
			a.PumpOverlays(animIdle)
			continue
		}
		t := clock().Sub(shown)
		idx, alpha, on := pulseAt(t, max(1, int(animPulseLength/animPulseFrame)))
		if !on {
			if pulse != nil {
				pulse.SetOpacity(0)
			}
			a.PumpOverlays(min(animIdle, untilNextPulse(t)))
			continue
		}
		if pulse == nil {
			pa = sc.pulseFrames()
			s := sc.res.Surface
			pb := Rect{X: s.X + int(pa.unit.X), Y: s.Y + int(pa.unit.Y), W: int(pa.unit.W), H: int(pa.unit.H)}
			if pulse, err = a.OpenOverlay(pb, pa.frames[idx], alpha); err != nil {
				pulse, reduce = nil, true // no pulse window: just stay shown
				continue
			}
		} else {
			_ = pulse.SetImage(pa.frames[idx])
			pulse.SetOpacity(alpha)
		}
		a.PumpOverlays(animPulseFrame)
	}

	// Exit: a plain fade of whatever is showing.
	pulseAlpha := 0.0
	if pulse != nil {
		if _, al, on := pulseAt(clock().Sub(shown), max(1, len(pa.frames))); on {
			pulseAlpha = al
		}
	}
	from := 1.0
	if reduce {
		from = clamp01(float64(clock().Sub(start)) / float64(animPlainFadeIn))
	}
	end := clock()
	for {
		k := 1 - clamp01(float64(clock().Sub(end))/float64(animExit))
		main.SetOpacity(from * k)
		if pulse != nil {
			pulse.SetOpacity(pulseAlpha * k)
		}
		if k <= 0 {
			break
		}
		a.PumpOverlays(animFrame)
	}
	return nil
}

// ---- docs: the entrance as a GIF -----------------------------------------

// RenderDrawGIF renders the entrance and one pulse, cropped to the
// drawing, over white, into an animated GIF (for docs). Nothing is shown.
func RenderDrawGIF(opts DrawOptions, outPath string) (DrawResult, error) {
	if outPath == "" {
		return DrawResult{}, errors.New("draw: output path is required")
	}
	sc, err := planDrawWith(defaultAdapter, opts)
	if err != nil {
		return DrawResult{}, err
	}
	g := sc.entranceGIF()
	f, err := os.Create(outPath)
	if err != nil {
		return sc.res, fmt.Errorf("draw: %w", err)
	}
	defer f.Close()
	if err := gif.EncodeAll(f, g); err != nil {
		return sc.res, fmt.Errorf("draw: encode gif: %w", err)
	}
	return sc.res, nil
}

func (sc *drawScene) entranceGIF() *gif.GIF {
	final := sc.render()
	pr, _ := sc.animCrop(final)
	if sc.text != nil {
		return sc.textGIF(final, pr)
	}
	pa := sc.pulseFrames()
	p0 := image.Pt(int(math.Round(pa.unit.X*sc.px)), int(math.Round(pa.unit.Y*sc.px)))
	pulseR := image.Rectangle{Min: p0, Max: p0.Add(pa.frames[0].Rect.Size())}
	r := pr.Union(pulseR)
	g := &gif.GIF{}
	add := func(img *image.RGBA, delay int, pulse *image.RGBA, alpha float64) {
		bg := image.NewRGBA(r)
		draw.Draw(bg, r, image.NewUniform(color.White), image.Point{}, draw.Src)
		if pulse != nil {
			draw.DrawMask(bg, pulseR, pulse, image.Point{},
				image.NewUniform(color.Alpha{uint8(alpha * 255)}), image.Point{}, draw.Over)
		}
		draw.Draw(bg, img.Rect, img, img.Rect.Min, draw.Over)
		pal := image.NewPaletted(image.Rect(0, 0, r.Dx(), r.Dy()), palette.Plan9)
		draw.FloydSteinberg.Draw(pal, pal.Rect, bg, r.Min)
		g.Image = append(g.Image, pal)
		g.Delay = append(g.Delay, delay)
	}
	step := 33 * time.Millisecond
	for t := time.Duration(0); t < animEntrance; t += step {
		add(sc.frame(pr, entrancePose(t, sc.lay.uiScale)), 3, nil, 0)
	}
	finalCrop := cropCopy(final, pr)
	add(finalCrop, int(animPulseFirst/(10*time.Millisecond)), nil, 0)
	for t := animPulseFirst; t < animPulseFirst+animPulseLength; t += step {
		idx, alpha, _ := pulseAt(t, len(pa.frames))
		add(finalCrop, 3, pa.frames[idx], alpha)
	}
	add(finalCrop, 100, nil, 0)
	return g
}

// textGIF is the text drawing's entrance (fade + slide), no pulse.
func (sc *drawScene) textGIF(final *image.RGBA, r image.Rectangle) *gif.GIF {
	g := &gif.GIF{}
	add := func(img *image.RGBA, delay int) {
		bg := image.NewRGBA(r)
		draw.Draw(bg, r, image.NewUniform(color.RGBA{0x55, 0x60, 0x6E, 0xFF}), image.Point{}, draw.Src)
		draw.Draw(bg, img.Rect, img, img.Rect.Min, draw.Over)
		pal := image.NewPaletted(image.Rect(0, 0, r.Dx(), r.Dy()), palette.Plan9)
		draw.FloydSteinberg.Draw(pal, pal.Rect, bg, r.Min)
		g.Image = append(g.Image, pal)
		g.Delay = append(g.Delay, delay)
	}
	for t := time.Duration(0); t < animEntrance; t += 33 * time.Millisecond {
		add(sc.frame(r, entrancePose(t, sc.lay.uiScale)), 3)
	}
	add(cropCopy(final, r), 150)
	return g
}
