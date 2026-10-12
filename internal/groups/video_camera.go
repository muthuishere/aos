package groups

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/engine"
)

// This file is the automatic camera behind `capture video zoom` and the
// render `capture video stop` does: events in, a deterministic ffmpeg filter
// out. It is pure — the same events always give the same expressions.
//
// The pipeline:
//
//  1. Global screen rects from the events log are mapped into the output
//     canvas (default 1920x1080) through the recorded capture rect and the
//     real video size, which absorbs any backing scale (Retina).
//  2. A director decides, at each instant, what the camera wants:
//     explicit focus > draw/find (and other action) events > follow
//     (cursor / window / smart) > wide.
//  3. A simulator moves the camera there: smoothstep eases for fixed targets,
//     a critically damped spring for moving ones (cursor, window).
//  4. The sampled path is simplified to linear keyframes, and each of zoom,
//     centre-x and centre-y becomes one flat ffmpeg expression in t, driving
//     scale(eval=frame) + crop.

// frect is a float rectangle in canvas pixels.
type frect struct{ X, Y, W, H float64 }

func (r frect) cx() float64 { return r.X + r.W/2 }
func (r frect) cy() float64 { return r.Y + r.H/2 }

// cam is a camera: the canvas point at the centre of the view, and the zoom.
type cam struct{ CX, CY, Z float64 }

// camGeom maps global screen coordinates into the canvas.
type camGeom struct {
	W, H    float64     // output canvas
	VW, VH  float64     // source video
	Capture engine.Rect // global rect the video shows; zero = video pixels
	s       float64     // video -> canvas fit scale
	ox, oy  float64     // letterbox offset
}

func newCamGeom(outW, outH, vw, vh int, capture engine.Rect) camGeom {
	g := camGeom{W: float64(outW), H: float64(outH), VW: float64(vw), VH: float64(vh), Capture: capture}
	g.s = math.Min(g.W/g.VW, g.H/g.VH)
	g.ox = (g.W - g.VW*g.s) / 2
	g.oy = (g.H - g.VH*g.s) / 2
	return g
}

// videoRect converts a global rect into source-video pixels. The scale comes
// from the video's real size over the captured rect's logical size, so a 2x
// Retina capture (3024 px wide video of a 1512 pt monitor) maps correctly.
func (g camGeom) videoRect(r engine.Rect) frect {
	if g.Capture.W <= 0 || g.Capture.H <= 0 {
		return frect{float64(r.X), float64(r.Y), float64(r.W), float64(r.H)}
	}
	sx, sy := g.VW/float64(g.Capture.W), g.VH/float64(g.Capture.H)
	return frect{float64(r.X-g.Capture.X) * sx, float64(r.Y-g.Capture.Y) * sy, float64(r.W) * sx, float64(r.H) * sy}
}

// canvasRect converts a global rect into canvas pixels.
func (g camGeom) canvasRect(r engine.Rect) frect {
	v := g.videoRect(r)
	return frect{g.ox + v.X*g.s, g.oy + v.Y*g.s, v.W * g.s, v.H * g.s}
}

func (g camGeom) canvasPoint(p engine.Point) (float64, float64) {
	r := g.canvasRect(engine.Rect{X: p.X, Y: p.Y})
	return r.X, r.Y
}

func (g camGeom) wide() cam { return cam{g.W / 2, g.H / 2, 1} }

// clamp keeps the zoom in [1, maxZoom] and the view inside the canvas.
func (g camGeom) clamp(c cam, maxZoom float64) cam {
	c.Z = math.Max(1, math.Min(c.Z, maxZoom))
	hw, hh := g.W/(2*c.Z), g.H/(2*c.Z)
	c.CX = math.Max(hw, math.Min(c.CX, g.W-hw))
	c.CY = math.Max(hh, math.Min(c.CY, g.H-hh))
	return c
}

// fit frames a rect: padded on every side, grown to the canvas aspect, zoom
// capped, view clamped to the frame.
func (g camGeom) fit(r frect, pad, maxZoom float64) cam {
	w, h := math.Max(r.W, 1)*(1+2*pad), math.Max(r.H, 1)*(1+2*pad)
	z := math.Min(g.W/w, g.H/h)
	return g.clamp(cam{r.cx(), r.cy(), z}, maxZoom)
}

// zoomOptions tune the camera. Zero values take the defaults.
type zoomOptions struct {
	OutW, OutH int
	Pad        float64 // padding around a target, as a fraction of its size
	MaxZoom    float64
	Ease       float64 // seconds to ease between fixed shots
	HoldWide   float64 // seconds the camera stays wide at the start
	Hold       float64 // seconds an action shot holds after its event
	Gap        float64 // events closer than this chain without going wide
	FollowZoom float64 // zoom for cursor follow
	Lag        float64 // follow lag in seconds
	Rate       float64 // camera samples per second
	Follow     string  // overrides the recording's follow mode when set
}

func (o zoomOptions) withDefaults() zoomOptions {
	def := func(v *float64, d float64) {
		if *v <= 0 {
			*v = d
		}
	}
	if o.OutW <= 0 || o.OutH <= 0 {
		o.OutW, o.OutH = 1920, 1080
	}
	def(&o.Pad, 0.15)
	def(&o.MaxZoom, 2.5)
	def(&o.Ease, 0.6)
	if o.HoldWide < 0 {
		o.HoldWide = 0
	} else if o.HoldWide == 0 {
		o.HoldWide = 1.0
	}
	def(&o.Hold, 2.0)
	def(&o.Gap, 3.0)
	def(&o.FollowZoom, 2.0)
	def(&o.Lag, 0.3)
	def(&o.Rate, 15)
	return o
}

// camKey is the camera at an instant.
type camKey struct {
	T float64
	C cam
}

type shot struct {
	T0, T1 float64
	C      cam
}

type ptSample struct {
	T    float64
	X, Y float64
}

type rectSample struct {
	T float64
	R frect
}

type followChange struct {
	T    float64
	Mode string
}

// director holds the timeline the camera reads from.
type director struct {
	g       camGeom
	o       zoomOptions
	focus   []shot
	actions []shot
	follow  []followChange
	cursor  []ptSample
	moved   []float64 // times the cursor actually moved
	windows []rectSample
	clicks  []float64
}

// isCameraAction reports whether an action event should steer the camera in
// a given follow mode. Smart mode already tracks windows and the cursor, so
// only pointing commands (draw, find) and marks take it over; follow-mouse and
// follow-app hand the camera to nobody but explicit focus.
func isCameraAction(ev videoEvent, mode string) bool {
	if ev.Rect == nil || ev.Rect.W <= 0 || ev.Rect.H <= 0 {
		return false
	}
	switch ev.Kind {
	case "mark":
	case "action", "click":
	default:
		return false
	}
	switch mode {
	case "", "off":
		return true
	case "smart":
		return ev.Kind == "mark" || strings.HasPrefix(ev.Cmd, "draw") || strings.HasPrefix(ev.Cmd, "find")
	}
	return false
}

func newDirector(doc videoDoc, g camGeom, o zoomOptions, dur float64) *director {
	d := &director{g: g, o: o}
	initial := doc.Follow
	if o.Follow != "" {
		initial = o.Follow
	}
	d.follow = []followChange{{0, initial}}
	events := append([]videoEvent(nil), doc.Events...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].T < events[j].T })
	for i := range events {
		events[i].T -= doc.Offset
	}

	// Explicit focus: until its hold, the next focus/unfocus, or the end.
	for i, ev := range events {
		if ev.Kind != "focus" || ev.Rect == nil {
			continue
		}
		end := dur
		if ev.Hold > 0 {
			end = ev.T + ev.Hold
		}
		for _, next := range events[i+1:] {
			if next.Kind == "focus" || next.Kind == "unfocus" {
				end = math.Min(end, next.T)
				break
			}
		}
		r := g.canvasRect(*ev.Rect)
		c := g.fit(r, o.Pad, o.MaxZoom)
		if ev.Zoom > 0 {
			c = g.clamp(cam{r.cx(), r.cy(), ev.Zoom}, math.Max(o.MaxZoom, ev.Zoom))
		}
		if end > ev.T {
			d.focus = append(d.focus, shot{ev.T, end, c})
		}
	}

	for _, ev := range events {
		switch ev.Kind {
		case "follow":
			if o.Follow == "" {
				d.follow = append(d.follow, followChange{ev.T, ev.Label})
			}
		case "cursor":
			if ev.At != nil {
				x, y := g.canvasPoint(*ev.At)
				if n := len(d.cursor); n == 0 || math.Hypot(x-d.cursor[n-1].X, y-d.cursor[n-1].Y) > 2 {
					d.moved = append(d.moved, ev.T)
				}
				d.cursor = append(d.cursor, ptSample{ev.T, x, y})
			}
		case "window":
			if ev.Rect != nil {
				d.windows = append(d.windows, rectSample{ev.T, g.canvasRect(*ev.Rect)})
			}
		}
		if ev.Kind == "click" {
			d.clicks = append(d.clicks, ev.T)
		}
	}

	// Action shots: arrive by the event, hold, chain to a close next event,
	// otherwise go back to wide.
	var acts []videoEvent
	for _, ev := range events {
		if isCameraAction(ev, d.modeAt(ev.T)) && !d.inFocus(ev.T) {
			acts = append(acts, ev)
		}
	}
	for i, ev := range acts {
		start := math.Max(ev.T-o.Ease, o.HoldWide)
		if n := len(d.actions); n > 0 {
			start = math.Max(start, d.actions[n-1].T0)
		}
		end := ev.T + o.Hold
		if i+1 < len(acts) && acts[i+1].T-ev.T <= o.Gap {
			end = math.Max(start, math.Max(acts[i+1].T-o.Ease, o.HoldWide))
		}
		end = math.Min(end, dur)
		if n := len(d.actions); n > 0 && d.actions[n-1].T1 > start {
			d.actions[n-1].T1 = start
		}
		if end > start {
			d.actions = append(d.actions, shot{start, end, g.fit(g.canvasRect(*ev.Rect), o.Pad, o.MaxZoom)})
		}
	}
	return d
}

func (d *director) inFocus(t float64) bool {
	for _, s := range d.focus {
		if t >= s.T0 && t < s.T1 {
			return true
		}
	}
	return false
}

func (d *director) modeAt(t float64) string {
	mode := ""
	for _, f := range d.follow {
		if f.T <= t {
			mode = f.Mode
		}
	}
	if mode == "off" {
		return ""
	}
	return mode
}

func (d *director) cursorAt(t float64) (ptSample, bool) {
	i := sort.Search(len(d.cursor), func(i int) bool { return d.cursor[i].T > t })
	if i == 0 {
		return ptSample{}, false
	}
	return d.cursor[i-1], true
}

func (d *director) windowAt(t float64) (frect, bool) {
	i := sort.Search(len(d.windows), func(i int) bool { return d.windows[i].T > t })
	if i == 0 {
		return frect{}, false
	}
	return d.windows[i-1].R, true
}

func (d *director) cursorActive(t float64) bool {
	i := sort.Search(len(d.moved), func(i int) bool { return d.moved[i] > t })
	return i > 0 && t-d.moved[i-1] <= 1.5
}

// want is what the camera wants at t: a key that changes when the target
// changes, the target, and whether it moves on its own (follow).
func (d *director) want(t float64) (string, cam, bool) {
	for i, s := range d.focus {
		if t >= s.T0 && t < s.T1 {
			return "focus" + strconv.Itoa(i), s.C, false
		}
	}
	for i, s := range d.actions {
		if t >= s.T0 && t < s.T1 {
			return "action" + strconv.Itoa(i), s.C, false
		}
	}
	g, o := d.g, d.o
	switch d.modeAt(t) {
	case "mouse":
		if p, ok := d.cursorAt(t); ok {
			return "follow", g.clamp(cam{p.X, p.Y, o.FollowZoom}, math.Max(o.MaxZoom, o.FollowZoom)), true
		}
	case "window", "app":
		if r, ok := d.windowAt(t); ok {
			return "follow", g.fit(r, o.Pad*0.3, o.MaxZoom), true
		}
	case "smart":
		r, ok := d.windowAt(t)
		if !ok {
			r = frect{0, 0, g.W, g.H}
		}
		win := g.fit(r, o.Pad*0.3, o.MaxZoom)
		large := r.W*r.H >= 0.4*g.W*g.H
		if p, ok := d.cursorAt(t); ok && large && d.cursorActive(t) {
			z := math.Min(o.MaxZoom, math.Max(win.Z*2, 1.8))
			return "follow", g.clamp(cam{p.X, p.Y, z}, o.MaxZoom), true
		}
		return "follow", win, true
	}
	return "wide", g.wide(), false
}

func smoothstep(u float64) float64 {
	u = math.Max(0, math.Min(1, u))
	return u * u * (3 - 2*u)
}

func lerpCam(a, b cam, u float64) cam {
	return cam{a.CX + (b.CX-a.CX)*u, a.CY + (b.CY-a.CY)*u, a.Z + (b.Z-a.Z)*u}
}

// springStep advances a critically damped spring by dt toward target. omega
// sets the lag; critical damping means it never overshoots.
func springStep(x, v, target, omega, dt float64) (float64, float64) {
	// Exact solution of x'' = omega^2 (target - x) - 2 omega x' over dt.
	e := math.Exp(-omega * dt)
	d0 := x - target
	tmp := (v + omega*d0) * dt
	nx := target + (d0+tmp)*e
	nv := (v - omega*tmp) * e
	return nx, nv
}

// deadZone keeps the follow target still until the new target is far enough
// away to be a real move rather than hand jitter.
func deadZone(held, next cam, radius float64) cam {
	if math.Hypot(next.CX-held.CX, next.CY-held.CY) <= radius && math.Abs(next.Z-held.Z) < 0.05 {
		return held
	}
	return next
}

// clickPulse is the brief extra zoom after a click: 1.0 -> 1.1 -> 1.0 over 0.5s.
func clickPulse(t float64, clicks []float64) float64 {
	k := 1.0
	for _, c := range clicks {
		if t >= c && t < c+0.5 {
			k = math.Max(k, 1+0.1*math.Sin(math.Pi*(t-c)/0.5))
		}
	}
	return k
}

// planCamera simulates the camera over [0, dur] at o.Rate samples per second.
func planCamera(doc videoDoc, g camGeom, o zoomOptions, dur float64) []camKey {
	o = o.withDefaults()
	d := newDirector(doc, g, o, dur)
	dt := 1 / o.Rate
	n := int(math.Ceil(dur*o.Rate)) + 1
	omega := 2.15 / o.Lag

	cur, vel := g.wide(), cam{}
	from, held := cur, cur
	prevKey, t0 := "wide", 0.0
	keys := make([]camKey, 0, n)
	for i := 0; i < n; i++ {
		t := math.Min(float64(i)*dt, dur)
		key, target, moving := d.want(t)
		if moving {
			if prevKey != key {
				vel, held = cam{}, target
			}
			held = deadZone(held, target, 0.03*g.W/held.Z)
			if i > 0 {
				cur.CX, vel.CX = springStep(cur.CX, vel.CX, held.CX, omega, dt)
				cur.CY, vel.CY = springStep(cur.CY, vel.CY, held.CY, omega, dt)
				cur.Z, vel.Z = springStep(cur.Z, vel.Z, held.Z, omega, dt)
			}
		} else {
			if key != prevKey {
				from, t0 = cur, t
			}
			cur = lerpCam(from, target, smoothstep((t-t0)/o.Ease))
			vel = cam{}
		}
		prevKey = key
		out := cur
		if mode := d.modeAt(t); moving && (mode == "mouse" || mode == "smart") {
			out.Z *= clickPulse(t, d.clicks)
		}
		keys = append(keys, camKey{t, g.clamp(out, math.Max(o.MaxZoom, o.FollowZoom)*1.1)})
	}
	return keys
}

// simplifyKeys drops samples a straight line between neighbours already
// reproduces within tol pixels (zoom is compared at tol/1000 per unit).
func simplifyKeys(keys []camKey, tol float64) []camKey {
	if len(keys) <= 2 {
		return keys
	}
	out := []camKey{keys[0]}
	a := 0
	for b := 2; b < len(keys); b++ {
		if !lineFits(keys, a, b, tol) {
			out = append(out, keys[b-1])
			a = b - 1
		}
	}
	return append(out, keys[len(keys)-1])
}

func lineFits(keys []camKey, a, b int, tol float64) bool {
	ka, kb := keys[a], keys[b]
	for k := a + 1; k < b; k++ {
		u := (keys[k].T - ka.T) / (kb.T - ka.T)
		p := lerpCam(ka.C, kb.C, u)
		c := keys[k].C
		if math.Abs(p.CX-c.CX) > tol || math.Abs(p.CY-c.CY) > tol || math.Abs(p.Z-c.Z) > tol/1000 {
			return false
		}
	}
	return true
}

func num(v float64) string {
	s := strconv.FormatFloat(v, 'f', 4, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// keyExpr writes one camera component as a flat ffmpeg expression in t: a sum
// of gated linear pieces, plus the last value held from the final key on.
// Flat (not nested if()) so a long recording does not hit parser depth.
func keyExpr(keys []camKey, get func(cam) float64) string {
	if len(keys) == 0 {
		return "0"
	}
	if len(keys) == 1 {
		return num(get(keys[0].C))
	}
	var terms []string
	for i := 0; i+1 < len(keys); i++ {
		a, b := keys[i], keys[i+1]
		if b.T <= a.T {
			continue
		}
		va, vb := get(a.C), get(b.C)
		gate := fmt.Sprintf("gte(t,%s)*lt(t,%s)", num(a.T), num(b.T))
		if num(va) == num(vb) {
			terms = append(terms, gate+"*"+num(va))
			continue
		}
		terms = append(terms, fmt.Sprintf("%s*(%s+%s*(t-%s))", gate, num(va), num((vb-va)/(b.T-a.T)), num(a.T)))
	}
	last := keys[len(keys)-1]
	terms = append(terms, fmt.Sprintf("gte(t,%s)*%s", num(last.T), num(get(last.C))))
	return strings.Join(terms, "+")
}

// cameraFilter builds the filtergraph: fit the source into the canvas, draw a
// ring at each click (follow modes), then zoom with scale(eval=frame) and pan
// with crop. Zoom is recovered inside crop as iw/W, so it is computed once.
func cameraFilter(keys []camKey, g camGeom, rings []frect, ringTimes []float64) string {
	W, H := int(g.W), int(g.H)
	z := keyExpr(keys, func(c cam) float64 { return c.Z })
	cx := keyExpr(keys, func(c cam) float64 { return c.CX })
	cy := keyExpr(keys, func(c cam) float64 { return c.CY })
	var b strings.Builder
	fmt.Fprintf(&b, "[0:v]scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1", W, H, W, H)
	for i, r := range rings {
		fmt.Fprintf(&b, ",drawbox=x=%s:y=%s:w=%s:h=%s:color=yellow@0.85:t=5:enable='between(t,%s,%s)'",
			num(r.X), num(r.Y), num(r.W), num(r.H), num(ringTimes[i]), num(ringTimes[i]+0.6))
	}
	fmt.Fprintf(&b, ",scale=w='trunc(%d*(%s)/2)*2':h='trunc(%d*(%s)/2)*2':eval=frame", W, z, H, z)
	fmt.Fprintf(&b, ",crop=w=%d:h=%d:x='clip((%s)*iw/%d-%d,0,iw-%d)':y='clip((%s)*ih/%d-%d,0,ih-%d)'",
		W, H, cx, W, W/2, W, cy, H, H/2, H)
	b.WriteString(",format=yuv420p[v]")
	return b.String()
}

// clickRings are the highlight boxes for clicks the follow camera pulses on.
func clickRings(doc videoDoc, g camGeom) ([]frect, []float64) {
	var rings []frect
	var times []float64
	for _, ev := range doc.Events {
		if ev.Kind != "click" || ev.Rect == nil || len(rings) >= 200 {
			continue
		}
		r := g.canvasRect(*ev.Rect)
		rings = append(rings, frect{r.cx() - 22, r.cy() - 22, 44, 44})
		times = append(times, ev.T-doc.Offset)
	}
	return rings, times
}

// renderArgs is the ffmpeg argv that renders the camera from a filter script.
func renderArgs(in, script, out string, hasAudio bool) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", in,
		"-filter_complex_script", script, "-map", "[v]",
		"-c:v", "libx264", "-preset", "medium", "-crf", "20"}
	if hasAudio {
		args = append(args, "-map", "0:a", "-c:a", "copy")
	}
	return append(args, "-movflags", "+faststart", out)
}
