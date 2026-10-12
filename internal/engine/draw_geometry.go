package engine

// Pure geometry for `windowctl draw`: where the sign goes, and the
// arrow from the sign to the target (flared root, bend/straight/zigzag/
// spiral shaft, notched head). A port of bigarrow's BigArrowCore
// (MIT, github.com/franzenzenhofer/big-arrow-on-the-screen). All values
// are in surface-local layout units, y down; no OS code.

import "math"

type vec struct{ X, Y float64 }

func (a vec) add(b vec) vec      { return vec{a.X + b.X, a.Y + b.Y} }
func (a vec) sub(b vec) vec      { return vec{a.X - b.X, a.Y - b.Y} }
func (a vec) mul(k float64) vec  { return vec{a.X * k, a.Y * k} }
func (a vec) dot(b vec) float64  { return a.X*b.X + a.Y*b.Y }
func (a vec) length() float64    { return math.Hypot(a.X, a.Y) }
func (a vec) dist(b vec) float64 { return b.sub(a).length() }
func (a vec) perp() vec          { return vec{-a.Y, a.X} }
func (a vec) norm() vec {
	if l := a.length(); l > 0 {
		return vec{a.X / l, a.Y / l}
	}
	return vec{}
}

type frect struct{ X, Y, W, H float64 }

func (r frect) minX() float64 { return r.X }
func (r frect) maxX() float64 { return r.X + r.W }
func (r frect) minY() float64 { return r.Y }
func (r frect) maxY() float64 { return r.Y + r.H }
func (r frect) center() vec   { return vec{r.X + r.W/2, r.Y + r.H/2} }
func (r frect) inset(dx, dy float64) frect {
	return frect{r.X + dx, r.Y + dy, r.W - 2*dx, r.H - 2*dy}
}
func (r frect) contains(o frect) bool {
	return o.minX() >= r.minX() && o.maxX() <= r.maxX() && o.minY() >= r.minY() && o.maxY() <= r.maxY()
}
func (r frect) intersect(o frect) (frect, bool) {
	x0, y0 := math.Max(r.minX(), o.minX()), math.Max(r.minY(), o.minY())
	x1, y1 := math.Min(r.maxX(), o.maxX()), math.Min(r.maxY(), o.maxY())
	if x1 <= x0 || y1 <= y0 {
		return frect{}, false
	}
	return frect{x0, y0, x1 - x0, y1 - y0}, true
}
func (r frect) union(o frect) frect {
	x0, y0 := math.Min(r.minX(), o.minX()), math.Min(r.minY(), o.minY())
	x1, y1 := math.Max(r.maxX(), o.maxX()), math.Max(r.maxY(), o.maxY())
	return frect{x0, y0, x1 - x0, y1 - y0}
}
func (r frect) clampedInto(b frect) frect {
	r.X = math.Min(math.Max(r.X, b.minX()), b.maxX()-r.W)
	r.Y = math.Min(math.Max(r.Y, b.minY()), b.maxY()-r.H)
	return r
}
func clampPoint(p vec, b frect) vec {
	return vec{math.Min(math.Max(p.X, b.minX()), b.maxX()), math.Min(math.Max(p.Y, b.minY()), b.maxY())}
}

// quad is a quadratic Bezier.
type quad struct{ a, c, b vec }

func (q quad) at(t float64) vec {
	u := 1 - t
	return q.a.mul(u * u).add(q.c.mul(2 * u * t)).add(q.b.mul(t * t))
}
func (q quad) tangent(t float64) vec {
	return q.c.sub(q.a).mul(2 * (1 - t)).add(q.b.sub(q.c).mul(2 * t)).norm()
}
func (q quad) points(n int) []vec {
	out := make([]vec, n+1)
	for i := 0; i <= n; i++ {
		out[i] = q.at(float64(i) / float64(n))
	}
	return out
}

func cubicPoints(p0, p1, p2, p3 vec, n int) []vec {
	out := make([]vec, 0, n)
	for i := 1; i <= n; i++ {
		t := float64(i) / float64(n)
		u := 1 - t
		out = append(out, p0.mul(u*u*u).add(p1.mul(3*u*u*t)).add(p2.mul(3*u*t*t)).add(p3.mul(t*t*t)))
	}
	return out
}

// ---- placement --------------------------------------------------------

const (
	placeKeepOutSide    = 120.0
	placeMargin         = 24.0
	placeShortcutCost   = 400.0
	placeOtherGap       = 12.0
	placeOverlapPenalty = 0.05
	// placeLengthCost per unit of sign-to-target distance: with room on
	// several sides, the closest sign (shortest arrow, least screen
	// covered) wins; diagonals with a wide sign are otherwise far away.
	placeLengthCost = 0.6
)

var placeReachScales = []float64{1.0, 0.75, 1.4, 0.55, 1.9, 2.5}

func keepOutZone(marked frect) frect {
	sq := frect{marked.center().X - placeKeepOutSide/2, marked.center().Y - placeKeepOutSide/2, placeKeepOutSide, placeKeepOutSide}
	return sq.union(marked.inset(-12, -12))
}

func support(w, h float64, u vec) float64 { return math.Abs(u.X)*w/2 + math.Abs(u.Y)*h/2 }

func signRectFor(w, h float64, keepOut frect, dir DrawFrom, reach float64) frect {
	u := drawFromUnits[dir]
	d := support(keepOut.W, keepOut.H, u) + reach + support(w, h, u)
	c := keepOut.center().add(u.mul(d))
	return frect{c.X - w/2, c.Y - h/2, w, h}
}

// rectGap is the distance between two rects (0 when they touch).
func rectGap(a, b frect) float64 {
	dx := math.Max(0, math.Max(a.minX()-b.maxX(), b.minX()-a.maxX()))
	dy := math.Max(0, math.Max(a.minY()-b.maxY(), b.minY()-a.maxY()))
	return math.Hypot(dx, dy)
}

func placeClearance(r, a frect) float64 {
	return math.Min(math.Min(math.Min(r.minX()-a.minX(), a.maxX()-r.maxX()), math.Min(r.minY()-a.minY(), a.maxY()-r.maxY())), 200)
}

// placeSign chooses the sign rect with the most free space around a
// marked area. A forced side is a preference: if the sign does not fit
// there, the roomiest other side wins, because an arrow must never hide
// what it points at. Signs of other live drawings are avoided while
// possible. Deterministic.
func placeSign(w, h float64, marked, area frect, reach float64, forced DrawFrom, others []frect) (frect, DrawFrom) {
	keepOut := keepOutZone(marked)
	usable := area.inset(placeMargin, placeMargin)
	padded := make([]frect, len(others))
	for i, o := range others {
		padded[i] = o.inset(-placeOtherGap, -placeOtherGap)
	}
	overlap := func(r frect) float64 {
		t := 0.0
		for _, o := range padded {
			if s, ok := r.intersect(o); ok {
				t += s.W * s.H
			}
		}
		return t
	}
	best := func(dirs []DrawFrom, tolerate bool) (frect, DrawFrom, bool) {
		bestScore := math.Inf(-1)
		var br frect
		var bd DrawFrom
		for rank, d := range dirs {
			for _, s := range placeReachScales {
				r := signRectFor(w, h, keepOut, d, reach*s)
				ov := overlap(r)
				if _, hit := r.intersect(keepOut); !usable.contains(r) || hit || (!tolerate && ov > 0) {
					continue
				}
				score := placeClearance(r, usable) - math.Abs(s-1)*placeShortcutCost - float64(rank)*4 - ov*placeOverlapPenalty -
					rectGap(r, keepOut)*placeLengthCost
				if score > bestScore {
					bestScore, br, bd = score, r, d
				}
			}
		}
		return br, bd, !math.IsInf(bestScore, -1)
	}
	var forcedDirs []DrawFrom
	if forced != "" {
		forcedDirs = []DrawFrom{forced}
	}
	for _, tolerate := range []bool{false, true} {
		for _, dirs := range [][]DrawFrom{forcedDirs, drawFromOrder} {
			if len(dirs) == 0 {
				continue
			}
			if r, d, ok := best(dirs, tolerate); ok {
				return r, d
			}
		}
	}
	// Nothing fits cleanly (tiny display or huge sign): stay on screen, overlap least.
	bestArea := math.Inf(1)
	var br frect
	var bd DrawFrom
	for _, d := range drawFromOrder {
		r := signRectFor(w, h, keepOut, d, reach).clampedInto(usable)
		a := 0.0
		if s, ok := r.intersect(keepOut); ok {
			a = s.W * s.H
		}
		if a < bestArea {
			bestArea, br, bd = a, r, d
		}
	}
	return br, bd
}

// ---- sign junction ----------------------------------------------------

// junction is where the shaft leaves the sign: a point on a straight
// part of the sign's edge, the edge's outward normal, and how wide the
// flare may be there.
type junction struct {
	point, normal vec
	halfWidth     float64
}

type signEdge struct {
	normal       vec
	point        func(float64) vec
	lo, hi, exit float64
}

func makeJunction(sign frect, radius float64, tip vec, stroke, preferredHalf float64) junction {
	inset := radius + 2
	hlo, hhi := sign.minX()+inset, math.Max(sign.maxX()-inset, sign.minX()+inset)
	vlo, vhi := sign.minY()+inset, math.Max(sign.maxY()-inset, sign.minY()+inset)
	// Where the ray from the centre to the tip leaves the rect.
	c := sign.center()
	d := tip.sub(c)
	sx, sy := math.Inf(1), math.Inf(1)
	if d.X != 0 {
		sx = (sign.W / 2) / math.Abs(d.X)
	}
	if d.Y != 0 {
		sy = (sign.H / 2) / math.Abs(d.Y)
	}
	exit := c.add(d.mul(math.Min(math.Min(sx, sy), 1)))
	top := signEdge{vec{0, -1}, func(a float64) vec { return vec{a, sign.minY()} }, hlo, hhi, exit.X}
	bottom := signEdge{vec{0, 1}, func(a float64) vec { return vec{a, sign.maxY()} }, hlo, hhi, exit.X}
	left := signEdge{vec{-1, 0}, func(a float64) vec { return vec{sign.minX(), a} }, vlo, vhi, exit.Y}
	right := signEdge{vec{1, 0}, func(a float64) vec { return vec{sign.maxX(), a} }, vlo, vhi, exit.Y}
	long := bottom
	if tip.Y < sign.center().Y {
		long = top
	}
	type cand struct {
		d float64
		e signEdge
	}
	cands := []cand{
		{math.Abs(exit.Y - sign.minY()), top}, {math.Abs(exit.Y - sign.maxY()), bottom},
		{math.Abs(exit.X - sign.minX()), left}, {math.Abs(exit.X - sign.maxX()), right},
	}
	crossed := cands[0]
	for _, k := range cands[1:] {
		if k.d < crossed.d {
			crossed = k
		}
	}
	edge := long
	if (crossed.e.hi-crossed.e.lo)/2 >= stroke*0.6 {
		edge = crossed.e
	}
	halfLen := (edge.hi - edge.lo) / 2
	half := math.Max(math.Min(preferredHalf, halfLen), 0)
	along := math.Min(math.Max(edge.exit, edge.lo+half), edge.hi-half)
	return junction{edge.point(along), edge.normal, half}
}

// ---- arrow ------------------------------------------------------------

const (
	arrowTailInset     = 12.0
	arrowBend          = 0.22
	arrowMinLeave      = 0.25
	zigzagRunIn        = 1.2
	zigzagLeadOut      = 3.5
	zigzagAmplitude    = 2.4
	zigzagLegLength    = 70.0
	rootBaseFactor     = 1.8
	rootFlareFactor    = 2.6
	rootDepth          = 10.0
	rootCornerInset    = 4.0
	rootShortShaft     = 9.0
	spiralLoopSamples  = 96
	spiralLegSamples   = 24
	spiralGapFactor    = 2.2
	spiralGrowthFactor = 2.8
	spiralRunIn        = 1.2
	spiralLegTangent   = 0.45
	spiralEntrySamples = 8
	spiralBorderAllow  = 6.0
)

// arrowGeom is everything the rasterizer needs to draw one arrow.
type arrowGeom struct {
	tip   vec
	shaft []vec // polyline, stroked with round caps/joins
	head  []vec // filled polygon
	root  []vec // filled polygon: the flare where the shaft grows out of the sign
}

func buildArrow(tip vec, sign frect, radius float64, bounds frect, m drawMetrics, shape DrawShape) arrowGeom {
	j := makeJunction(sign, radius, tip, m.stroke, m.stroke*rootBaseFactor)
	tail := j.point.sub(j.normal.mul(arrowTailInset))
	var control vec
	var joints []vec
	switch shape {
	case ShapeStraight:
		control = tail.add(tip.sub(tail).mul(0.5))
	case ShapeZigzag:
		leadOut := j.point.add(j.normal.mul(m.stroke * zigzagLeadOut))
		end := tip.sub(tip.sub(leadOut).norm().mul(m.headLength * zigzagRunIn))
		joints = append([]vec{leadOut}, zigzagJoints(leadOut, end, j.normal, m.stroke)...)
		joints = append(joints, end)
		control = end
	case ShapeSpiral:
		control, joints = spiralRoute(sign, j, tip, m, bounds)
	default:
		control = bendControl(tail, tip, j.normal, bounds.inset(m.stroke, m.stroke))
	}
	dir := tip.sub(control).norm()
	base := tip.sub(dir.mul(m.headLength))
	side := dir.perp().mul(m.headWidth / 2)
	shaftEnd := tip.sub(dir.mul(m.headLength * 0.7))
	g := arrowGeom{tip: tip}
	g.head = []vec{tip, base.add(side), tip.sub(dir.mul(m.headLength * 0.78)), base.sub(side)}
	curve := quad{tail, control, shaftEnd}
	firstLeg := curve
	if len(joints) == 0 {
		g.shaft = curve.points(48)
	} else {
		g.shaft = append(append([]vec{tail}, joints...), shaftEnd)
		firstLeg = quad{tail, tail.add(joints[0].sub(tail).mul(0.5)), joints[0]}
	}
	g.root = rootPolygon(j, firstLeg, m.stroke)
	return g
}

func bendControl(tail, tip, normal vec, bounds frect) vec {
	chord := tip.sub(tail)
	mid := tail.add(chord.mul(0.5))
	off := chord.perp().norm().mul(chord.length() * arrowBend)
	clear := func(p vec) float64 {
		return math.Min(math.Min(p.X-bounds.minX(), bounds.maxX()-p.X), math.Min(p.Y-bounds.minY(), bounds.maxY()-p.Y))
	}
	best := mid.add(off)
	if alt := mid.sub(off); clear(alt) > clear(best) {
		best = alt
	}
	if out, want := best.sub(tail).dot(normal), chord.length()*arrowMinLeave; out < want {
		best = best.add(normal.mul(want - out))
	}
	return clampPoint(best, bounds)
}

func zigzagJoints(tail, end, normal vec, stroke float64) []vec {
	chord := end.sub(tail)
	length := chord.length()
	legs := int(math.Round(length / zigzagLegLength))
	if legs%2 == 0 {
		legs++
	}
	legs = min(max(legs, 3), 7)
	unit := chord.norm()
	side := unit.perp()
	amp := math.Min(stroke*zigzagAmplitude, length/float64(legs)*0.6)
	var out []vec
	for i := 1; i < legs; i++ {
		swing := 1.0
		if i%2 == 0 {
			swing = -1
		}
		out = append(out, tail.add(unit.mul(length*float64(i)/float64(legs))).add(side.mul(amp*swing)))
	}
	// The first leg must head away from the sign: mirror across the chord if not.
	if len(out) > 0 && out[0].sub(tail).dot(normal) < 0 {
		for i, p := range out {
			rel := p.sub(tail)
			along := unit.mul(rel.dot(unit))
			out[i] = tail.add(along).sub(rel.sub(along))
		}
	}
	return out
}

// spiralPadding is the room a spiral's loop needs on each side of a sign.
func spiralPadding(w, h float64, m drawMetrics) (float64, float64) {
	extra := m.stroke*(spiralGapFactor+spiralGrowthFactor) + m.stroke/2 + spiralBorderAllow
	k := math.Sqrt2 - 1
	return w/2*k + extra, h/2*k + extra
}

func spiralRoute(sign frect, j junction, tip vec, m drawMetrics, bounds frect) (vec, []vec) {
	heading := func(loop []vec) float64 {
		n := len(loop)
		return loop[n-1].sub(loop[n-2]).norm().dot(tip.sub(loop[n-1]).norm())
	}
	loop := spiralLoop(sign, j, m, 1)
	if alt := spiralLoop(sign, j, m, -1); heading(alt) > heading(loop) {
		loop = alt
	}
	exit := loop[len(loop)-1]
	end := tip.sub(tip.sub(exit).norm().mul(m.headLength * spiralRunIn))
	tangent := exit.sub(loop[len(loop)-2]).norm()
	leg := quad{exit, exit.add(tangent.mul(exit.dist(end) * spiralLegTangent)), end}
	pts := append(loop, leg.points(spiralLegSamples)[1:]...)
	for i := range pts {
		pts[i] = clampPoint(pts[i], bounds)
	}
	return pts[len(pts)-1], pts
}

func spiralLoop(sign frect, j junction, m drawMetrics, turn float64) []vec {
	c := sign.center()
	gap, growth := m.stroke*spiralGapFactor, m.stroke*spiralGrowthFactor
	rx, ry := sign.W/2*math.Sqrt2+gap, sign.H/2*math.Sqrt2+gap
	off := j.point.sub(c)
	start := math.Atan2(off.Y/ry, off.X/rx)
	ring := make([]vec, spiralLoopSamples+1)
	for i := range ring {
		share := float64(i) / spiralLoopSamples
		a := start + turn*2*math.Pi*share
		g := growth * share
		ring[i] = c.add(vec{(rx + g) * math.Cos(a), (ry + g) * math.Sin(a)})
	}
	stub := j.point.add(j.normal.mul(m.stroke))
	entry := quad{stub, ring[0], ring[spiralEntrySamples]}
	var out []vec
	for i := 0; i < spiralEntrySamples; i++ {
		out = append(out, entry.at(float64(i)/spiralEntrySamples))
	}
	return append(out, ring[spiralEntrySamples:]...)
}

// rootPolygon is the flared join between sign and shaft, like a
// speech-bubble tail: two cubic flanks leave the sign along its edge
// and run into the shaft's sides parallel to the shaft.
func rootPolygon(j junction, shaft quad, stroke float64) []vec {
	scale := math.Min(1, shaft.a.dist(shaft.b)/(stroke*rootShortShaft))
	flare := stroke * rootFlareFactor * scale
	lo, hi := 0.0, 1.0
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		if shaft.at(mid).dist(j.point) < flare {
			lo = mid
		} else {
			hi = mid
		}
	}
	t := (lo + hi) / 2
	neck, tangent := shaft.at(t), shaft.tangent(t)
	side := tangent.perp()
	along := j.normal.perp()
	orient := 1.0
	if along.dot(side) < 0 {
		orient = -1
	}
	base := math.Max(math.Min(j.halfWidth*scale, j.halfWidth), stroke/2)
	edge := j.point.sub(j.normal.mul(rootCornerInset))
	type flank struct{ onEdge, nearEdge, nearShaft, onShaft vec }
	mk := func(sgn float64) flank {
		ss := neck.add(side.mul(stroke / 2 * sgn * orient))
		return flank{
			edge.add(along.mul(base * sgn)), edge.add(along.mul(base * 0.35 * sgn)),
			ss.sub(tangent.mul(flare * 0.55)), ss,
		}
	}
	f0, f1 := mk(orient), mk(-orient)
	poly := []vec{f0.onEdge.sub(j.normal.mul(rootDepth)), f0.onEdge}
	poly = append(poly, cubicPoints(f0.onEdge, f0.nearEdge, f0.nearShaft, f0.onShaft, 16)...)
	poly = append(poly, f1.onShaft)
	poly = append(poly, cubicPoints(f1.onShaft, f1.nearShaft, f1.nearEdge, f1.onEdge, 16)...)
	return append(poly, f1.onEdge.sub(j.normal.mul(rootDepth)))
}

// ---- layout -----------------------------------------------------------

type markKind int

const (
	markNone markKind = iota
	markBox
	markRing
)

type drawMark struct {
	kind   markKind
	rect   frect // box
	center vec   // ring: an ellipse, a circle for point targets
	rx, ry float64
}

func (m drawMark) area() (frect, bool) {
	switch m.kind {
	case markBox:
		return m.rect, true
	case markRing:
		return frect{m.center.X - m.rx, m.center.Y - m.ry, m.rx * 2, m.ry * 2}, true
	}
	return frect{}, false
}

// drawLayout is everything drawn on one surface.
type drawLayout struct {
	mark    drawMark
	sign    frect
	radius  float64
	from    DrawFrom
	arrow   arrowGeom
	metrics drawMetrics
	uiScale float64
	// anchor is the target point (a rect's centre); pointTarget says the
	// target was a point.
	anchor      vec
	pointTarget bool
}

// planLayout places the sign and builds the arrow. target is a point
// when isPoint, else a rect; area is the usable surface region and
// bounds the full surface, both surface-local.
func planLayout(target frect, isPoint bool, area, bounds frect, signW, signH float64, o DrawOptions, ui float64, others []frect) drawLayout {
	m := o.Size.metrics().scaled(ui)
	anchor := target.center()
	if isPoint {
		anchor = vec{target.X, target.Y}
	}
	var mk drawMark
	// bigarrow's 8pt: just over half the outline (6pt line + 6pt border),
	// so the box never covers the text it is highlighting.
	pad := 8 * ui
	switch {
	case o.Style == DrawRing && isPoint:
		mk = drawMark{kind: markRing, center: anchor, rx: 36 * ui, ry: 36 * ui}
	case o.Style == DrawRing:
		// Hug the rect like a pen-drawn oval: the smallest ellipse with the
		// rect's aspect that contains it (semi-axes √2 × half sides), plus
		// a margin so the line clears the glyphs.
		m := 10 * ui
		mk = drawMark{kind: markRing, center: anchor, rx: target.W/2*math.Sqrt2 + m, ry: target.H/2*math.Sqrt2 + m}
	case !isPoint:
		mk = drawMark{kind: markBox, rect: target.inset(-pad, -pad)}
	case o.Style == DrawBox:
		mk = drawMark{kind: markBox, rect: frect{anchor.X - 36*ui, anchor.Y - 36*ui, 72 * ui, 72 * ui}}
	}
	marked, ok := mk.area()
	if !ok {
		marked = frect{anchor.X, anchor.Y, 0, 0}
	}
	pw, ph := 0.0, 0.0
	if o.Shape == ShapeSpiral {
		pw, ph = spiralPadding(signW, signH, m)
	}
	placed, from := placeSign(signW+2*pw, signH+2*ph, marked, area, m.reach, o.From, others)
	sign := placed.inset(pw, ph)
	radius := 0.0
	if o.Corners == CornersRound {
		radius = math.Min(sign.H/2, 30*ui)
	}
	// Point targets are hit exactly; boxes at the edge midpoint nearest
	// the sign; rings at the point facing the sign.
	gap := 6 * ui
	tip := anchor
	switch mk.kind {
	case markRing:
		// The ellipse point in the sign's direction, then the gap.
		d := sign.center().sub(mk.center).norm()
		k := 1 / math.Hypot(d.X/mk.rx, d.Y/mk.ry)
		tip = mk.center.add(d.mul(k + gap))
	case markBox:
		r := mk.rect
		mids := [][2]vec{
			{{r.center().X, r.minY()}, {0, -1}}, {{r.center().X, r.maxY()}, {0, 1}},
			{{r.minX(), r.center().Y}, {-1, 0}}, {{r.maxX(), r.center().Y}, {1, 0}},
		}
		best := mids[0]
		for _, c := range mids[1:] {
			if c[0].dist(sign.center()) < best[0].dist(sign.center()) {
				best = c
			}
		}
		tip = best[0].add(best[1].mul(gap))
	}
	return drawLayout{
		mark: mk, sign: sign, radius: radius, from: from, metrics: m, uiScale: ui, anchor: anchor, pointTarget: isPoint,
		arrow: buildArrow(tip, sign, radius, bounds, m, o.Shape),
	}
}
