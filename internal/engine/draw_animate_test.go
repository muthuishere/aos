package engine

import (
	"bytes"
	"encoding/json"
	"image"
	"image/gif"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/engine/core"
)

func animScene(t *testing.T, o DrawOptions) *drawScene {
	t.Helper()
	o.NoConfig = true
	if o.Text == "" {
		o.Text = "Click here"
	}
	sc, err := planDrawWith(drawMock(), o)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func opaqueCount(img *image.RGBA) int {
	n := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			n++
		}
	}
	return n
}

var animTargets = map[string]DrawOptions{
	"arrow-point": {Point: &Point{900, 600}},
	"ring-point":  {Point: &Point{900, 600}, Style: DrawRing},
	"box-rect":    {Region: &Rect{X: 700, Y: 500, W: 300, H: 80}},
	"ring-rect":   {Region: &Rect{X: 700, Y: 500, W: 300, H: 80}, Style: DrawRing},
	"spiral":      {Point: &Point{900, 600}, Shape: ShapeSpiral},
	"zigzag-blk":  {Point: &Point{400, 300}, Shape: ShapeZigzag, Border: BorderBlack},
}

// The last entrance frame is exactly the static drawing (what --preview
// renders), so the drawing never jumps when the animation settles.
func TestEntranceEndsOnTheStaticFrame(t *testing.T) {
	for name, o := range animTargets {
		sc := animScene(t, o)
		final := sc.render()
		pr, _ := sc.animCrop(final)
		want := cropCopy(final, pr)
		got := sc.frame(pr, entrancePose(animEntrance, sc.lay.uiScale))
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Errorf("%s: settled frame differs from the static render", name)
		}
		if opaqueCount(want) != opaqueCount(final) {
			t.Errorf("%s: crop cut off part of the drawing", name)
		}
	}
}

// The stroke reveals progressively: frames grow monotonically from
// (almost) nothing to the full drawing.
func TestEntranceRevealsProgressively(t *testing.T) {
	for name, o := range animTargets {
		sc := animScene(t, o)
		final := sc.render()
		pr, _ := sc.animCrop(final)
		full := opaqueCount(cropCopy(final, pr))
		first := opaqueCount(sc.frame(pr, entrancePose(0, sc.lay.uiScale)))
		mid := opaqueCount(sc.frame(pr, entrancePose(animEntrance/3, sc.lay.uiScale)))
		if first != 0 {
			t.Errorf("%s: frame 0 should be empty (sign at alpha 0, stroke unrevealed), has %d px", name, first)
		}
		if !(mid > 0 && mid < full) {
			t.Errorf("%s: mid-entrance frame has %d of %d px", name, mid, full)
		}
	}
}

func TestEntrancePose(t *testing.T) {
	p0 := entrancePose(0, 1)
	if p0.reveal != 0 || p0.signAlpha != 0 || p0.signDY != animSignSlide || p0.markScale != animMarkFrom {
		t.Errorf("pose(0) = %+v", p0)
	}
	prev := p0
	for ms := 10; ms <= 450; ms += 10 {
		p := entrancePose(time.Duration(ms)*time.Millisecond, 1)
		if p.reveal < prev.reveal || p.signAlpha < prev.signAlpha || p.signDY > prev.signDY || p.markScale < prev.markScale {
			t.Fatalf("pose not monotonic at %dms: %+v after %+v", ms, p, prev)
		}
		prev = p
	}
	if prev != finalPose {
		t.Errorf("pose(450ms) = %+v, want final", prev)
	}
}

func TestTruncatePolyline(t *testing.T) {
	pts := []vec{{0, 0}, {10, 0}, {10, 10}}
	got := truncatePolyline(pts, 15)
	if len(got) != 3 || got[2] != (vec{10, 5}) {
		t.Errorf("truncate 15 = %v", got)
	}
	if truncatePolyline(pts, 0) != nil {
		t.Error("truncate 0 should be empty")
	}
	if l := polylineLength(truncatePolyline(pts, 100)); l != 20 {
		t.Errorf("over-long truncate length %v", l)
	}
}

func TestPulseSchedule(t *testing.T) {
	if _, _, on := pulseAt(0, 27); on {
		t.Error("no pulse straight after the entrance")
	}
	idx, a, on := pulseAt(animPulseFirst, 27)
	if !on || idx != 0 || a != animPulseAlpha {
		t.Errorf("pulse start = %d %.2f %v", idx, a, on)
	}
	if _, _, on := pulseAt(animPulseFirst+animPulseLength+time.Millisecond, 27); on {
		t.Error("pulse should rest between beats")
	}
	if _, _, on := pulseAt(animPulseFirst+animPulsePeriod+10*time.Millisecond, 27); !on {
		t.Error("pulse should repeat every period")
	}
	if d := untilNextPulse(animPulseFirst + animPulseLength); d != animPulsePeriod-animPulseLength {
		t.Errorf("untilNextPulse = %v", d)
	}
	sc := animScene(t, animTargets["ring-point"])
	pa := sc.pulseFrames()
	if len(pa.frames) != int(animPulseLength/animPulseFrame) {
		t.Errorf("pulse frames = %d", len(pa.frames))
	}
	c, r0 := sc.pulseSpec()
	if c != sc.lay.mark.center || r0 != sc.lay.mark.rx {
		t.Errorf("ring-point pulse should ring the circle, got %v r=%v", c, r0)
	}
	// The biggest ring still fits its window.
	last := pa.frames[len(pa.frames)-1]
	b := opaqueBounds(last)
	if b.Min.X <= 0 || b.Min.Y <= 0 || b.Max.X >= last.Rect.Max.X || b.Max.Y >= last.Rect.Max.Y {
		t.Errorf("pulse ring clipped: %v in %v", b, last.Rect)
	}
}

func TestPreviewGIF(t *testing.T) {
	sc := animScene(t, animTargets["arrow-point"])
	g := sc.entranceGIF()
	want := int(animEntrance/(33*time.Millisecond)) + 1 + int(animPulseLength/(33*time.Millisecond)) + 1 + 1
	if len(g.Image) < want-2 || len(g.Image) > want+2 {
		t.Errorf("gif frames = %d, want ~%d", len(g.Image), want)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
}

// ---- the show loop against a fake animator ----

type fakeOverlay struct {
	b       Rect
	images  int
	opacity []float64
	closed  bool
}

func (o *fakeOverlay) SetImage(*image.RGBA) error { o.images++; return nil }
func (o *fakeOverlay) SetOpacity(a float64)       { o.opacity = append(o.opacity, a) }
func (o *fakeOverlay) Close()                     { o.closed = true }

type fakeAnimator struct {
	*mockAdapter
	reduce   bool
	overlays []*fakeOverlay
	pumps    int
	shown    int
}

func (f *fakeAnimator) OpenOverlay(b Rect, img *image.RGBA, op float64) (core.Overlay, error) {
	o := &fakeOverlay{b: b, opacity: []float64{op}}
	f.overlays = append(f.overlays, o)
	return o, nil
}
func (f *fakeAnimator) PumpOverlays(d time.Duration) { f.pumps++; time.Sleep(d) }
func (f *fakeAnimator) ReduceMotion() bool           { return f.reduce }
func (f *fakeAnimator) ShowOverlay(_ Rect, _ *image.RGBA, done func() bool) error {
	f.shown++
	for !done() {
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

func shortPulse(t *testing.T) {
	old := []time.Duration{animPulseFirst, animPulsePeriod}
	animPulseFirst, animPulsePeriod = 50*time.Millisecond, 1100*time.Millisecond
	t.Cleanup(func() { animPulseFirst, animPulsePeriod = old[0], old[1] })
}

func TestAnimatedDrawLifecycle(t *testing.T) {
	shortPulse(t)
	f := &fakeAnimator{mockAdapter: drawMock()}
	o := DrawOptions{Text: "x", Point: &Point{900, 600}, Duration: 1200 * time.Millisecond, NoConfig: true}
	start := time.Now()
	res, err := drawWith(f, o)
	if err != nil {
		t.Fatal(err)
	}
	if f.shown != 0 || len(f.overlays) != 2 {
		t.Fatalf("shown=%d overlays=%d, want the animated path with a main + pulse window", f.shown, len(f.overlays))
	}
	main, pulse := f.overlays[0], f.overlays[1]
	if !main.closed || !pulse.closed {
		t.Error("windows must be closed")
	}
	// Entrance frames at ~60fps for 450ms, then nothing until the fade.
	if main.images < 10 || main.images > 40 {
		t.Errorf("main window got %d images, want ~27 entrance frames", main.images)
	}
	if last := main.opacity[len(main.opacity)-1]; last != 0 {
		t.Errorf("main window should end faded out, at %v", last)
	}
	if el := time.Since(start); el < o.Duration+animExit {
		t.Errorf("returned after %v, before duration + fade", el)
	}
	// Main window covers the drawing, not the whole monitor.
	if main.b.W >= res.Surface.W || main.b.H >= res.Surface.H {
		t.Errorf("main window %v should be cropped inside %v", main.b, res.Surface)
	}
	if pulse.images == 0 || pulse.images > 30 {
		t.Errorf("pulse frames = %d, want one beat at 30fps", pulse.images)
	}
}

func TestAnimatedDrawReducedMotion(t *testing.T) {
	shortPulse(t)
	f := &fakeAnimator{mockAdapter: drawMock(), reduce: true}
	if _, err := drawWith(f, DrawOptions{Text: "x", Point: &Point{900, 600}, Duration: 700 * time.Millisecond, NoConfig: true}); err != nil {
		t.Fatal(err)
	}
	if len(f.overlays) != 1 {
		t.Fatalf("reduce motion: %d windows, want no pulse window", len(f.overlays))
	}
	m := f.overlays[0]
	if m.images != 0 {
		t.Errorf("reduce motion should not redraw frames, got %d", m.images)
	}
	if m.opacity[0] != 0 || m.opacity[len(m.opacity)-1] != 0 {
		t.Errorf("reduce motion should fade in from 0 and out to 0: %v", m.opacity)
	}
	peak := 0.0
	for _, v := range m.opacity {
		peak = max(peak, v)
	}
	if peak != 1 {
		t.Errorf("fade-in peaks at %v", peak)
	}
}

func TestAnimateOffAndStop(t *testing.T) {
	f := &fakeAnimator{mockAdapter: drawMock()}
	if _, err := drawWith(f, DrawOptions{Text: "x", Point: &Point{5, 5}, Duration: 30 * time.Millisecond, NoConfig: true, Animate: AnimateOff}); err != nil {
		t.Fatal(err)
	}
	if f.shown != 1 || len(f.overlays) != 0 {
		t.Errorf("--animate=off should use the static overlay: shown=%d overlays=%d", f.shown, len(f.overlays))
	}
	// Stop during the entrance skips straight to the fade-out.
	stop := make(chan struct{})
	close(stop)
	start := time.Now()
	if _, err := drawWith(f, DrawOptions{Text: "x", Point: &Point{5, 5}, Stop: stop, NoConfig: true}); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > animExit+300*time.Millisecond {
		t.Errorf("stopped drawing took %v to leave", el)
	}
	if len(f.overlays) != 1 || !f.overlays[0].closed {
		t.Errorf("stopped drawing: %d windows", len(f.overlays))
	}
	if _, err := drawWith(f, DrawOptions{Text: "x", Point: &Point{5, 5}, NoConfig: true, Animate: "sometimes"}); err == nil {
		t.Error("bad --animate value should fail")
	}
}

func TestAnimateConfigKey(t *testing.T) {
	for raw, want := range map[string]DrawAnimate{`{"animate":false}`: AnimateOff, `{"animate":"off"}`: AnimateOff, `{"animate":true}`: AnimateOn, `{}`: ""} {
		var c DrawConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(raw, err)
		}
		var o DrawOptions
		if err := o.applyConfig(c); err != nil {
			t.Fatal(err)
		}
		if o.Animate != want {
			t.Errorf("%s -> %q, want %q", raw, o.Animate, want)
		}
	}
	o := DrawOptions{Animate: AnimateOn}
	_ = o.applyConfig(DrawConfig{Animate: "off"})
	if o.Animate != AnimateOn {
		t.Error("the flag wins over the config")
	}
}
