package engine

import (
	"errors"
	"image"

	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func drawMock() *mockAdapter {
	return &mockAdapter{
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: 2, X: 1920, Y: 0, Width: 1280, Height: 800},
		},
		windows: []Window{{ID: "w1", Title: "Inbox — Mail", App: "Mail", Bounds: Rect{X: 2000, Y: 100, W: 600, H: 400}}},
	}
}

func red(t *testing.T) *Color {
	c, err := ParseColor("red")
	if err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestParseColorPresetsAndHex(t *testing.T) {
	for raw, want := range map[string]Color{
		"#FFFFFF": {1, 1, 1}, "fff": {1, 1, 1}, "BLACK": {0x1C / 255.0, 0x1C / 255.0, 0x1E / 255.0},
	} {
		got, err := ParseColor(raw)
		if err != nil || got != want {
			t.Errorf("ParseColor(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := ParseColor("chartreuse-ish"); err == nil || !strings.Contains(err.Error(), "preset") {
		t.Errorf("bad colour should list presets, got %v", err)
	}
}

// Every preset's sign text must be readable: AA (4.5:1) except the two
// hues no text colour reaches it on, which still clear 3:1 (AA large —
// sign text is 30pt+ heavy).
func TestPresetTextContrast(t *testing.T) {
	for _, p := range colorPresets {
		c, _ := ParseColor(p.name)
		ratio := contrastRatio(c, c.contrast())
		if ratio < 3 {
			t.Errorf("%s: text contrast %.2f < 3", p.name, ratio)
		}
	}
	r, _ := ParseColor("red")
	if got := contrastRatio(r, r.contrast()); got < 4.5 {
		t.Errorf("default red text contrast %.2f < 4.5", got)
	}
}

func TestDrawValidatesEnums(t *testing.T) {
	o := DrawOptions{Text: "x", Point: &Point{10, 10}, Shape: "wiggly", NoConfig: true}
	if _, _, err := renderDrawWith(drawMock(), o); err == nil || !strings.Contains(err.Error(), "bend, straight, zigzag, spiral") {
		t.Fatalf("want enum error listing shapes, got %v", err)
	}
}

func TestDrawNeedsExactlyOneTargetAndText(t *testing.T) {
	a := drawMock()
	if _, _, err := renderDrawWith(a, DrawOptions{Text: "x", NoConfig: true}); err == nil {
		t.Error("no target should fail")
	}
	if _, _, err := renderDrawWith(a, DrawOptions{Text: "x", Point: &Point{1, 1}, Mouse: true, NoConfig: true}); err == nil {
		t.Error("two targets should fail")
	}
	if _, _, err := renderDrawWith(a, DrawOptions{Point: &Point{1, 1}, NoConfig: true}); err == nil {
		t.Error("missing text should fail")
	}
}

// The arrow tip lands exactly on a point target, the sign is on the
// same monitor, fully inside it, and never covers the target.
func TestDrawPointTipIsExactAndSignClear(t *testing.T) {
	img, res, err := renderDrawWith(drawMock(), DrawOptions{Text: "Click here", Point: &Point{700, 500}, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tip != (Point{700, 500}) || res.Monitor != 1 {
		t.Fatalf("tip/monitor = %+v/%d", res.Tip, res.Monitor)
	}
	s := res.Sign
	if s.X < 0 || s.Y < 0 || s.X+s.W > 1920 || s.Y+s.H > 1080 {
		t.Errorf("sign %+v leaves the monitor", s)
	}
	if 700 >= s.X && 700 <= s.X+s.W && 500 >= s.Y && 500 <= s.Y+s.H {
		t.Errorf("sign %+v covers the target", s)
	}
	if img.Rect.Dx() != 1920 || img.Rect.Dy() != 1080 {
		t.Errorf("image %v, want monitor size", img.Rect)
	}
	// The head is painted right at the tip, the sign body at its centre.
	if a := img.RGBAAt(697, 500).A; a == 0 {
		t.Error("nothing painted at the arrow tip")
	}
	if a := img.RGBAAt(s.X+s.W/2, s.Y+4).A; a != 255 {
		t.Errorf("sign body not opaque (alpha %d)", a)
	}
	if a := img.RGBAAt(5, 5).A; a != 0 {
		t.Error("far corner should stay transparent")
	}
}

// --monitor makes --at relative, and the drawing lands on that monitor.
func TestDrawMonitorRelativePoint(t *testing.T) {
	m := 2
	_, res, err := renderDrawWith(drawMock(), DrawOptions{Text: "x", Point: &Point{100, 100}, Monitor: &m, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Monitor != 2 || res.Tip != (Point{2020, 100}) || res.Surface.X != 1920 {
		t.Fatalf("got %+v", res)
	}
}

func TestDrawWindowTargetBoxesTheWindow(t *testing.T) {
	_, res, err := renderDrawWith(drawMock(), DrawOptions{Text: "This window", Match: &Match{App: "mail"}, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Monitor != 2 {
		t.Fatalf("monitor = %d", res.Monitor)
	}
	// Box marks are hit at an edge midpoint, just outside the padded box.
	b := Rect{X: 2000 - 8, Y: 100 - 8, W: 616, H: 416}
	onEdge := res.Tip.X == b.X+b.W/2 || res.Tip.Y == b.Y+b.H/2
	if !onEdge {
		t.Errorf("tip %+v is not on an edge midpoint of %+v", res.Tip, b)
	}
	if _, _, err := renderDrawWith(drawMock(), DrawOptions{Text: "x", Match: &Match{App: "Nope"}, NoConfig: true}); !errors.Is(err, ErrNoMatch) {
		t.Errorf("unmatched window: %v", err)
	}
}

func TestDrawForcedSideAndAvoid(t *testing.T) {
	_, res, err := renderDrawWith(drawMock(), DrawOptions{Text: "x", Point: &Point{900, 500}, From: "left", NoConfig: true})
	if err != nil || res.From != "left" || res.Sign.X+res.Sign.W > 900 {
		t.Fatalf("forced left: %+v %v", res, err)
	}
	first := res.Sign
	_, res2, err := renderDrawWith(drawMock(), DrawOptions{Text: "x", Point: &Point{900, 500}, From: "left", Avoid: []Rect{first}, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, hit := (frect{float64(first.X), float64(first.Y), float64(first.W), float64(first.H)}).intersect(
		frect{float64(res2.Sign.X), float64(res2.Sign.Y), float64(res2.Sign.W), float64(res2.Sign.H)}); hit {
		t.Errorf("second sign %+v overlaps the first %+v", res2.Sign, first)
	}
}

func TestDrawAllShapesAndStylesRender(t *testing.T) {
	for _, shape := range []DrawShape{ShapeBend, ShapeStraight, ShapeZigzag, ShapeSpiral} {
		for _, style := range []DrawStyle{DrawArrow, DrawRing, DrawBox} {
			for _, border := range []DrawBorder{BorderShadow, BorderWhiteBlack, BorderBlack} {
				_, _, err := renderDrawWith(drawMock(), DrawOptions{
					Text: "a fairly long sign that has to wrap onto more than one line to fit", Point: &Point{960, 540},
					Shape: shape, Style: style, Border: border, Color: red(t), NoConfig: true,
				})
				if err != nil {
					t.Errorf("%s/%s/%s: %v", shape, style, border, err)
				}
			}
		}
	}
}

func TestDrawConfigFillsOnlyUnsetFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fontconfig.json")
	if err := os.WriteFile(p, []byte(`{"color":"blue","shape":"zigzag","size":"L"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var cfg DrawConfig
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AOS_CONFIG", p)
	if DrawConfigPath() != p {
		t.Fatalf("path = %s", DrawConfigPath())
	}
	cfg = DrawConfig{Color: "blue", Shape: "zigzag", Size: "L"}
	o := DrawOptions{Size: "S"}
	if err := o.applyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	blue, _ := ParseColor("blue")
	if o.Size != "S" || o.Shape != "zigzag" || o.Color == nil || *o.Color != blue {
		t.Errorf("got %+v", o)
	}
	if err := (&DrawOptions{}).applyConfig(DrawConfig{Color: "nope"}); err == nil {
		t.Error("bad colour in config should fail")
	}
}

func TestDrawShowsUntilDuration(t *testing.T) {
	a := &overlayRecorder{mockAdapter: drawMock()}
	start := time.Now()
	if _, err := drawWith(a, DrawOptions{Text: "x", Point: &Point{5, 5}, Duration: 60 * time.Millisecond, NoConfig: true}); err != nil {
		t.Fatal(err)
	}
	if a.shown != 1 || time.Since(start) < 60*time.Millisecond {
		t.Errorf("shown=%d after %v", a.shown, time.Since(start))
	}
	stop := make(chan struct{})
	close(stop)
	if _, err := drawWith(a, DrawOptions{Text: "x", Point: &Point{5, 5}, Stop: stop, NoConfig: true}); err != nil {
		t.Fatal(err)
	}
}

// overlayRecorder pumps done() like a real adapter would.
type overlayRecorder struct {
	*mockAdapter
	shown int
}

func (o *overlayRecorder) ShowOverlay(_ Rect, _ *image.RGBA, done func() bool) error {
	o.shown++
	for !done() {
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

// Runes the sign font lacks fall back to the embedded font instead of
// rendering as missing-glyph boxes.
func TestSignTextFallsBackForMissingGlyphs(t *testing.T) {
	primary, _ := opentype.Parse(goregular.TTF)
	st := layoutText(primary, "a\U0001F600b", 40, 2000)
	runs := st.runs("a\U0001F600b")
	if len(runs) != 3 || runs[1].face != st.fallback || runs[0].face != st.face {
		t.Fatalf("runs = %d, want primary/fallback/primary split", len(runs))
	}
	if r := st.runs("plain text"); len(r) != 1 {
		t.Errorf("covered text should be one run, got %d", len(r))
	}
}

func TestDrawConfigPathPrefersAosThenLegacyWindowctl(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AOS_CONFIG", "")
	t.Setenv("WINDOWCTL_CONFIG", "")
	primary := filepath.Join(dir, "aos", "draw.json")
	legacy := filepath.Join(dir, "windowctl", "fontconfig.json")
	if got := DrawConfigPath(); got != primary {
		t.Fatalf("nothing on disk: got %s, want %s", got, primary)
	}
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DrawConfigPath(); got != legacy {
		t.Fatalf("legacy only: got %s, want %s", got, legacy)
	}
	envLegacy := filepath.Join(dir, "env.json")
	t.Setenv("WINDOWCTL_CONFIG", envLegacy)
	if got := DrawConfigPath(); got != envLegacy {
		t.Fatalf("WINDOWCTL_CONFIG: got %s, want %s", got, envLegacy)
	}
	if err := os.MkdirAll(filepath.Dir(primary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(primary, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DrawConfigPath(); got != primary {
		t.Fatalf("aos file present: got %s, want %s", got, primary)
	}
	t.Setenv("AOS_CONFIG", envLegacy)
	if got := DrawConfigPath(); got != envLegacy {
		t.Fatalf("AOS_CONFIG: got %s, want %s", got, envLegacy)
	}
}
