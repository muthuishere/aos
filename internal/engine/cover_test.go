package engine

import (
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/engine/core"
)

func mustLook(t *testing.T, s CoverStyle) coverLook {
	t.Helper()
	l, err := s.look()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestParseCoverColorAlpha(t *testing.T) {
	c, a, err := ParseCoverColor("#1e40af")
	if err != nil || a != 1 || rgba(c) != (color.RGBA{0x1e, 0x40, 0xaf, 255}) {
		t.Fatalf("#1e40af = %v %v %v", c, a, err)
	}
	_, a, err = ParseCoverColor("#00000080")
	if err != nil || a < 0.5 || a > 0.51 {
		t.Fatalf("#00000080 alpha = %v %v", a, err)
	}
	if _, _, err := ParseCoverColor("#12345"); err == nil {
		t.Error("bad colour accepted")
	}
	if tint, _ := (CoverStyle{Color: "#000000cc"}).Tint(); !tint {
		t.Error("alpha < 1 must be a tint")
	}
	if tint, _ := (CoverStyle{Color: "blue"}).Tint(); tint {
		t.Error("opaque colour is not a tint")
	}
}

// Every pixel of a plain cover is the fill, fully opaque.
func TestRenderCoverOpaqueEveryPixel(t *testing.T) {
	for _, px := range []float64{1, 2} {
		img := renderCover(mustLook(t, CoverStyle{}), 123, 45, px, nil)
		if img.Rect.Dx() != int(123*px) || img.Rect.Dy() != int(45*px) {
			t.Fatalf("size %v at px %v", img.Rect, px)
		}
		for i := 0; i < len(img.Pix); i += 4 {
			if img.Pix[i] != 0 || img.Pix[i+1] != 0 || img.Pix[i+2] != 0 || img.Pix[i+3] != 255 {
				t.Fatalf("pixel %d = %v, want opaque black", i/4, img.Pix[i:i+4])
			}
		}
	}
}

func TestRenderCoverBlueWithCentredWhiteText(t *testing.T) {
	img := renderCover(mustLook(t, CoverStyle{Color: "#1e40af", Text: "Hidden by request", TextColor: "white"}), 600, 200, 1, nil)
	fill := color.RGBA{0x1e, 0x40, 0xaf, 255}
	var n, sx, sy int
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			p := img.RGBAAt(x, y)
			if p.A != 255 {
				t.Fatalf("pixel %d,%d not opaque: %v", x, y, p)
			}
			if p.R > 0xe0 && p.G > 0xe0 && p.B > 0xe0 {
				n, sx, sy = n+1, sx+x, sy+y
			} else if p != fill && (p.R < fill.R || p.B < 0x80) {
				t.Fatalf("pixel %d,%d = %v is neither fill nor text", x, y, p)
			}
		}
	}
	if n < 200 {
		t.Fatalf("only %d white text pixels", n)
	}
	cx, cy := sx/n, sy/n
	if cx < 270 || cx > 330 || cy < 80 || cy > 120 {
		t.Errorf("text centred at %d,%d, want near 300,100", cx, cy)
	}
}

// Long text in a small cover shrinks to fit and stays inside the margin.
func TestRenderCoverTextAutoFits(t *testing.T) {
	l := mustLook(t, CoverStyle{Text: strings.Repeat("Account number 1234 5678 ", 6), Size: "XL", TextColor: "white"})
	img := renderCover(l, 160, 60, 2, nil)
	text := 0
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			p := img.RGBAAt(x, y)
			if p.A != 255 {
				t.Fatalf("pixel %d,%d not opaque", x, y)
			}
			if p.R == 0 {
				continue
			}
			text++
			// The margin (6% of the short side, >= 2) is kept clear.
			if x < 4 || y < 4 || x >= img.Rect.Dx()-4 || y >= img.Rect.Dy()-4 {
				t.Fatalf("text pixel at %d,%d spills into the edge", x, y)
			}
		}
	}
	if text == 0 {
		t.Fatal("no text drawn")
	}
}

func TestRenderCoverBorderAndHoles(t *testing.T) {
	l := mustLook(t, CoverStyle{Color: "black", Border: 4, BorderColor: "#ff0000"})
	img := renderCover(l, 100, 100, 1, []frect{{50, 50, 50, 50}})
	if p := img.RGBAAt(1, 50); p.R < 200 || p.A != 255 {
		t.Errorf("border pixel = %v, want red", p)
	}
	if p := img.RGBAAt(20, 20); p != (color.RGBA{0, 0, 0, 255}) {
		t.Errorf("inside pixel = %v, want black", p)
	}
	if p := img.RGBAAt(75, 75); p.A != 0 {
		t.Errorf("hole pixel = %v, want transparent", p)
	}
	if p := img.RGBAAt(49, 75); p.A != 255 {
		t.Errorf("pixel next to the hole = %v, want covered", p)
	}
}

func TestParseCoverRectAndPercent(t *testing.T) {
	r, err := ParseCoverRect("10,20,50%,25%")
	if err != nil {
		t.Fatal(err)
	}
	got := r.in(Rect{X: 100, Y: 100, W: 800, H: 400})
	if got != (Rect{X: 110, Y: 120, W: 400, H: 100}) {
		t.Errorf("in = %v", got)
	}
	for _, bad := range []string{"1,2,3", "a,0,1,1", "0,0,0,10"} {
		if _, err := ParseCoverRect(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func coverMock() *mockAdapter {
	m := drawMock()
	m.windows = []Window{
		{ID: "w9", Title: "front", App: "Notes", Bounds: Rect{X: 0, Y: 0, W: 50, H: 50}},
		{ID: "w1", Title: "id.png", App: "Preview", Bounds: Rect{X: 200, Y: 100, W: 600, H: 400}},
	}
	return m
}

func TestPlanCoverTargets(t *testing.T) {
	m := coverMock()
	preview := &Match{App: "Preview"}
	pct, _ := ParseCoverRect("0,0,50%,100%")
	px, _ := ParseCoverRect("10,10,100,50")
	two := 2
	for name, tc := range map[string]struct {
		o    CoverOptions
		want Rect
	}{
		"whole window":   {CoverOptions{Match: preview}, Rect{X: 200, Y: 100, W: 600, H: 400}},
		"window percent": {CoverOptions{Match: preview, Rect: &pct}, Rect{X: 200, Y: 100, W: 300, H: 400}},
		"window points":  {CoverOptions{Match: preview, Rect: &px}, Rect{X: 210, Y: 110, W: 100, H: 50}},
		"global rect":    {CoverOptions{Rect: &px}, Rect{X: 10, Y: 10, W: 100, H: 50}},
		"monitor":        {CoverOptions{Monitor: &two}, Rect{X: 1920, Y: 0, W: 1280, H: 800}},
		"monitor rect":   {CoverOptions{Monitor: &two, Rect: &pct}, Rect{X: 1920, Y: 0, W: 640, H: 800}},
	} {
		tc.o.NoConfig = true
		p, err := planCoverWith(m, tc.o)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if p.rect != tc.want {
			t.Errorf("%s: rect %v, want %v", name, p.rect, tc.want)
		}
	}
	for name, o := range map[string]CoverOptions{
		"no target":       {},
		"percent alone":   {Rect: &pct},
		"find no window":  {Find: "x"},
		"missing window":  {Match: &Match{App: "Nope"}},
		"bad colour":      {Rect: &px, Style: CoverStyle{Color: "nope"}},
		"unknown preset":  {Rect: &px, Preset: "nope"},
		"bad position":    {Rect: &px, Style: CoverStyle{Position: "middle-ish"}},
		"window+monitor":  {Match: preview, Monitor: &two},
		"find and a rect": {Match: preview, Find: "x", Rect: &px},
	} {
		o.NoConfig = true
		if _, err := planCoverWith(m, o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := planCoverWith(m, CoverOptions{Match: &Match{App: "Nope"}, NoConfig: true}); !errors.Is(err, ErrNoMatch) {
		t.Errorf("missing window error %v should wrap ErrNoMatch (exit 3)", err)
	}
}

func TestPlanCoverFindPadsAndFollows(t *testing.T) {
	m := coverMock()
	m.axTexts = map[string][]TextItem{"w1": {{Text: "ghp_abcdef", Role: "text", Bounds: Rect{X: 300, Y: 200, W: 120, H: 18}}}}
	p, err := planCoverWith(m, CoverOptions{Match: &Match{App: "Preview"}, Find: "ghp_", Pad: 4, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.rect != (Rect{X: 296, Y: 196, W: 128, H: 26}) {
		t.Errorf("found rect %v", p.rect)
	}
	if got := p.place(Rect{X: 250, Y: 150, W: 600, H: 400}); got != (Rect{X: 346, Y: 246, W: 128, H: 26}) {
		t.Errorf("moved with the window to %v", got)
	}
	m.axTexts = nil
	if _, err := planCoverWith(m, CoverOptions{Match: &Match{App: "Preview"}, Find: "ghp_", FindSource: FindAccessibility, NoConfig: true}); err == nil {
		t.Error("missing text accepted")
	}
}

func TestCoverPresets(t *testing.T) {
	p, err := planCoverWith(coverMock(), CoverOptions{Rect: &CoverRect{W: coverLen{V: 10}, H: coverLen{V: 10}}, Preset: "private",
		Style: CoverStyle{TextColor: "yellow"}, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.style.Text != "Private" || p.style.Color != "#000000" || p.style.TextColor != "yellow" {
		t.Errorf("preset + flags = %+v", p.style)
	}
}

// ---- the follow loop against a fake cover window ----

type fakeCoverWin struct {
	updates []Rect
	images  []*image.RGBA
	visible []bool
	closed  bool
}

func (w *fakeCoverWin) Update(b Rect, img *image.RGBA) error {
	w.updates, w.images = append(w.updates, b), append(w.images, img)
	return nil
}
func (w *fakeCoverWin) SetVisible(v bool) { w.visible = append(w.visible, v) }
func (w *fakeCoverWin) Close()            { w.closed = true }

type fakeCoverer struct {
	*mockAdapter
	win   *fakeCoverWin
	first *image.RGBA
	open  Rect
	block bool
}

func (f *fakeCoverer) OpenCover(b Rect, img *image.RGBA, block bool) (core.CoverWindow, error) {
	f.win, f.first, f.open, f.block = &fakeCoverWin{}, img, b, block
	return f.win, nil
}
func (f *fakeCoverer) PumpOverlays(time.Duration) {}

func startCover(t *testing.T, f *fakeCoverer, o CoverOptions) *coverRun {
	t.Helper()
	o.NoConfig = true
	p, err := planCoverWith(f, o)
	if err != nil {
		t.Fatal(err)
	}
	r := &coverRun{a: f, co: f, p: p}
	if err := r.open(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCoverFollowsMoveResizeAndMinimise(t *testing.T) {
	f := &fakeCoverer{mockAdapter: coverMock()}
	pct, _ := ParseCoverRect("0,0,50%,50%")
	var moves []CoverResult
	r := startCover(t, f, CoverOptions{Match: &Match{App: "Preview"}, Rect: &pct, BlockInput: true,
		Moved: func(c CoverResult) { moves = append(moves, c) }})
	if f.open != (Rect{X: 200, Y: 100, W: 300, H: 200}) || !f.block {
		t.Fatalf("opened at %v block=%v", f.open, f.block)
	}
	// Nothing changed: no redraw.
	_ = r.tick()
	if len(f.win.updates) != 0 {
		t.Fatalf("idle tick redrew: %v", f.win.updates)
	}
	// Moved and resized: one update, at the new place and size.
	f.windows[1].Bounds = Rect{X: 1000, Y: 300, W: 800, H: 600}
	_ = r.tick()
	if len(f.win.updates) != 1 || f.win.updates[0] != (Rect{X: 1000, Y: 300, W: 400, H: 300}) {
		t.Fatalf("after move: %v", f.win.updates)
	}
	if img := f.win.images[0]; img.Rect.Dx() != 400 || img.Rect.Dy() != 300 {
		t.Errorf("image %v not resized with the window", img.Rect)
	}
	// Minimised (gone from the on-screen list): hidden.
	win := f.windows[1]
	f.windows = f.windows[:1]
	_ = r.tick()
	if len(f.win.visible) != 1 || f.win.visible[0] || !r.res.Hidden {
		t.Fatalf("after minimise: visible=%v hidden=%v", f.win.visible, r.res.Hidden)
	}
	// Restored somewhere else: redrawn in place, then shown.
	win.Bounds = Rect{X: 0, Y: 600, W: 400, H: 200}
	f.windows = append(f.windows, win)
	_ = r.tick()
	if n := len(f.win.updates); f.win.updates[n-1] != (Rect{X: 0, Y: 600, W: 200, H: 100}) {
		t.Fatalf("after restore: %v", f.win.updates)
	}
	if !f.win.visible[len(f.win.visible)-1] || r.res.Hidden {
		t.Fatal("not shown again after restore")
	}
	if len(moves) < 4 {
		t.Errorf("Moved called %d times", len(moves))
	}
}

// A window list that cannot be read keeps the cover exactly where it is.
func TestCoverStaysPutWhenTheListFails(t *testing.T) {
	f := &fakeCoverer{mockAdapter: coverMock()}
	r := startCover(t, f, CoverOptions{Match: &Match{App: "Preview"}})
	f.listErr = errors.New("window server busy")
	_ = r.tick()
	if len(f.win.updates) != 0 || len(f.win.visible) != 0 {
		t.Fatalf("list error changed the cover: updates %v visible %v", f.win.updates, f.win.visible)
	}
}

// A window in front of the target is left uncovered; the target is not.
func TestCoverClipsWindowsInFront(t *testing.T) {
	f := &fakeCoverer{mockAdapter: coverMock()}
	f.windows[0].Bounds = Rect{X: 700, Y: 400, W: 300, H: 300} // overlaps the cover's bottom-right
	_ = startCover(t, f, CoverOptions{Match: &Match{App: "Preview"}})
	img := f.first
	if p := img.RGBAAt(550, 350); p.A != 0 {
		t.Errorf("pixel under the front window = %v, want transparent", p)
	}
	if p := img.RGBAAt(450, 250); p.A != 255 {
		t.Errorf("pixel of the target = %v, want covered", p)
	}
	// A window behind the target never cuts the cover.
	f2 := &fakeCoverer{mockAdapter: coverMock()}
	f2.windows = []Window{f2.windows[1], {ID: "w9", App: "Notes", Bounds: Rect{X: 700, Y: 400, W: 300, H: 300}}}
	_ = startCover(t, f2, CoverOptions{Match: &Match{App: "Preview"}})
	if p := f2.first.RGBAAt(550, 350); p.A != 255 {
		t.Errorf("window behind cut the cover: %v", p)
	}
}

func TestCoverNoFollowPinsIt(t *testing.T) {
	f := &fakeCoverer{mockAdapter: coverMock()}
	r := startCover(t, f, CoverOptions{Match: &Match{App: "Preview"}, NoFollow: true})
	f.windows = nil
	_ = r.tick()
	if len(f.win.updates) != 0 || len(f.win.visible) != 0 {
		t.Fatal("--no-follow cover moved or hid")
	}
}

// An update swaps the look in a single Update call (never hide + show),
// and a bad update keeps the old look.
func TestCoverUpdateSwapsInOneFrame(t *testing.T) {
	f := &fakeCoverer{mockAdapter: coverMock()}
	var pending *CoverStyle
	shown := 0
	r := startCover(t, f, CoverOptions{Rect: &CoverRect{W: coverLen{V: 100}, H: coverLen{V: 40}},
		Updates:     func() *CoverStyle { p := pending; pending = nil; return p },
		UpdateShown: func() { shown++ }})
	pending = &CoverStyle{Color: "#1e40af", Text: "Back soon"}
	_ = r.tick()
	if len(f.win.updates) != 1 || len(f.win.visible) != 0 || shown != 1 {
		t.Fatalf("updates %d visible %v shown %d", len(f.win.updates), f.win.visible, shown)
	}
	if p := f.win.images[0].RGBAAt(2, 2); p != (color.RGBA{0x1e, 0x40, 0xaf, 255}) {
		t.Errorf("updated fill = %v", p)
	}
	pending = &CoverStyle{Color: "not-a-colour"}
	_ = r.tick()
	if len(f.win.updates) != 1 || shown != 2 || r.p.style.Color != "#1e40af" {
		t.Fatalf("bad update applied: %d updates, style %+v", len(f.win.updates), r.p.style)
	}
}

func TestCoverWithoutAdapterSupport(t *testing.T) {
	_, err := coverWith(coverMock(), CoverOptions{Rect: &CoverRect{W: coverLen{V: 1}, H: coverLen{V: 1}}, NoConfig: true})
	if !errors.Is(err, core.ErrNotImplemented) {
		t.Fatalf("err = %v", err)
	}
}

func TestCoverRunsUntilStop(t *testing.T) {
	f := &fakeCoverer{mockAdapter: coverMock()}
	stop := make(chan struct{})
	time.AfterFunc(30*time.Millisecond, func() { close(stop) })
	res, err := coverWith(f, CoverOptions{Match: &Match{App: "Preview"}, Stop: stop, NoConfig: true, ID: "scan"})
	if err != nil {
		t.Fatal(err)
	}
	if !f.win.closed || res.ID != "scan" || res.Window == nil || res.Window.ID != "w1" {
		t.Fatalf("closed=%v res=%+v", f.win.closed, res)
	}
}

// The rounded corners of a window in front stay covered: the covered
// window would show through them.
func TestCoverHoleKeepsCornersCovered(t *testing.T) {
	l := mustLook(t, CoverStyle{})
	img := renderCover(l, 200, 200, 2, []frect{{100, 100, 100, 100}})
	for _, c := range []struct {
		x, y int
		a    uint8
	}{{201, 201, 255}, {399, 399, 255}, {201, 399, 255}, {300, 300, 0}, {300, 202, 0}, {202, 300, 0}, {199, 300, 255}} {
		if p := img.RGBAAt(c.x, c.y); p.A != c.a {
			t.Errorf("pixel %d,%d alpha %d, want %d", c.x, c.y, p.A, c.a)
		}
	}
}
