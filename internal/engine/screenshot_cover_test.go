package engine

import (
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// desk is a synthetic "screen": every pixel a distinct, non-black colour,
// so a pixel that escaped redaction is easy to see.
func desk(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, color.RGBA{uint8(40 + x%200), uint8(40 + y%200), 200, 255})
		}
	}
	return img
}

func shotMock(scale int) *mockAdapter {
	m := coverMock()
	m.monitors = []Monitor{{ID: 1, X: 0, Y: 0, Width: 400, Height: 300, Primary: true}}
	m.windows = []Window{
		{ID: "w1", Title: "id.png", App: "Preview", Bounds: Rect{X: 100, Y: 50, W: 120, H: 80}},
		{ID: "w2", Title: "zsh", App: "Terminal", Bounds: Rect{X: 0, Y: 150, W: 400, H: 150}},
	}
	m.captureImg = desk(400*scale, 300*scale)
	return m
}

func readBack(t *testing.T, path string) *image.RGBA {
	t.Helper()
	img, err := readPNG(path)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// assertCovered checks every pixel of r (global units, capture at 0,0)
// is c, and the pixel just outside each edge is not.
func assertCovered(t *testing.T, img *image.RGBA, r Rect, scale int, c color.RGBA) {
	t.Helper()
	for y := r.Y * scale; y < (r.Y+r.H)*scale; y++ {
		for x := r.X * scale; x < (r.X+r.W)*scale; x++ {
			if p := img.RGBAAt(x, y); p != c {
				t.Fatalf("pixel %d,%d = %v, want cover %v", x, y, p, c)
			}
		}
	}
	for _, p := range []image.Point{{r.X*scale - 1, r.Y * scale}, {(r.X + r.W) * scale, r.Y * scale}} {
		if p.In(img.Rect) && img.RGBAAt(p.X, p.Y) == c {
			t.Errorf("pixel %v outside the cover was painted", p)
		}
	}
}

func noFilesLeft(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	if len(ents) != 0 {
		t.Fatalf("files left in %s: %v", dir, ents)
	}
}

func TestScreenshotCoverRectReadsBackBlack(t *testing.T) {
	for _, scale := range []int{1, 2} {
		m := shotMock(scale)
		out := filepath.Join(t.TempDir(), "s.png")
		res, err := screenshotCoveredWith(m, ScreenshotOptions{OutPath: out,
			Covers: []ScreenCover{{Rect: &Rect{X: 10, Y: 20, W: 30, H: 40}}}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Rect != (Rect{W: 400, H: 300}) || len(res.Covered) != 1 {
			t.Fatalf("res %+v", res)
		}
		img := readBack(t, out)
		assertCovered(t, img, Rect{X: 10, Y: 20, W: 30, H: 40}, scale, color.RGBA{0, 0, 0, 255})
		if p := img.RGBAAt(200*scale, 200*scale); p == (color.RGBA{0, 0, 0, 255}) {
			t.Error("pixel far from the cover was painted")
		}
		if filepath.Dir(m.capturedPath) == filepath.Dir(out) {
			t.Error("raw capture must not land next to the output")
		}
		if _, err := os.Stat(m.capturedPath); !os.IsNotExist(err) {
			t.Error("raw unredacted capture left on disk")
		}
	}
}

func TestScreenshotCoverWindowFindAndColour(t *testing.T) {
	m := shotMock(1)
	m.axTexts = map[string][]TextItem{"w2": {{Text: "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789", Role: "text", Source: "accessibility", Bounds: Rect{X: 20, Y: 200, W: 250, H: 16}}}}
	m.ocrTexts = []TextItem{{Text: "IBAN DE89 3704", Source: "ocr", Bounds: Rect{X: 300, Y: 10, W: 80, H: 12}}}
	out := filepath.Join(t.TempDir(), "s.png")
	res, err := screenshotCoveredWith(m, ScreenshotOptions{OutPath: out, Covers: []ScreenCover{
		{Match: &Match{App: "Preview"}, Style: CoverStyle{Color: "#1e40af"}},
		{Match: &Match{App: "Terminal"}, Find: "ghp_", Pad: 2},
		{Find: "IBAN"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Covered) != 3 {
		t.Fatalf("covered %v", res.Covered)
	}
	img := readBack(t, out)
	assertCovered(t, img, Rect{X: 100, Y: 50, W: 120, H: 80}, 1, color.RGBA{0x1e, 0x40, 0xaf, 255})
	assertCovered(t, img, Rect{X: 18, Y: 198, W: 254, H: 20}, 1, color.RGBA{0, 0, 0, 255})
	assertCovered(t, img, Rect{X: 300, Y: 10, W: 80, H: 12}, 1, color.RGBA{0, 0, 0, 255})
	if m.ocrRect != (Rect{W: 400, H: 300}) {
		t.Errorf("screen-wide find scanned %v, want the capture", m.ocrRect)
	}
}

// A cover with text reads back exactly as the rendered cover, and every
// pixel in it is opaque: the text never lets the screen through.
func TestScreenshotCoverWithTextIsTheRenderedCover(t *testing.T) {
	m := shotMock(2)
	out := filepath.Join(t.TempDir(), "s.png")
	style := CoverStyle{Color: "#1e40af", Text: "Hidden by request", TextColor: "white", Radius: 12}
	if _, err := screenshotCoveredWith(m, ScreenshotOptions{OutPath: out,
		Covers: []ScreenCover{{Rect: &Rect{X: 50, Y: 50, W: 200, H: 60}, Style: style}}}); err != nil {
		t.Fatal(err)
	}
	img := readBack(t, out)
	l, _ := style.look()
	l.radius = 0
	want := renderCover(l, 200, 60, 2, nil)
	white := 0
	for y := 0; y < 120; y++ {
		for x := 0; x < 400; x++ {
			got := img.RGBAAt(100+x, 100+y)
			if got != want.RGBAAt(x, y) || got.A != 255 {
				t.Fatalf("pixel %d,%d = %v, want %v", x, y, got, want.RGBAAt(x, y))
			}
			if got.R > 0xe0 && got.G > 0xe0 {
				white++
			}
		}
	}
	if white == 0 {
		t.Error("no text in the cover")
	}
}

func TestScreenshotCoverPartlyOutsideIsClipped(t *testing.T) {
	m := shotMock(1)
	out := filepath.Join(t.TempDir(), "s.png")
	region := Rect{X: 50, Y: 50, W: 100, H: 100}
	m.captureImg = desk(100, 100)
	if _, err := screenshotCoveredWith(m, ScreenshotOptions{OutPath: out, Region: &region,
		Covers: []ScreenCover{{Rect: &Rect{X: 0, Y: 0, W: 80, H: 80}}, {Rect: &Rect{X: 300, Y: 300, W: 5, H: 5}}}}); err != nil {
		t.Fatal(err)
	}
	assertCovered(t, readBack(t, out), Rect{X: 0, Y: 0, W: 30, H: 30}, 1, color.RGBA{0, 0, 0, 255})
}

// Fail closed: nothing is written, and nothing is even captured when a
// cover cannot be placed.
func TestScreenshotCoverFailsClosed(t *testing.T) {
	for name, tc := range map[string]struct {
		cover   ScreenCover
		setup   func(*mockAdapter)
		notHere bool // error wraps ErrNoMatch (exit 3)
	}{
		"text not on screen":   {cover: ScreenCover{Find: "ghp_"}, notHere: true},
		"text not in window":   {cover: ScreenCover{Match: &Match{App: "Terminal"}, Find: "ghp_"}, setup: func(m *mockAdapter) { m.axTexts = map[string][]TextItem{} }, notHere: true},
		"window missing":       {cover: ScreenCover{Match: &Match{App: "1Password"}}, notHere: true},
		"tint":                 {cover: ScreenCover{Rect: &Rect{W: 10, H: 10}, Style: CoverStyle{Color: "#000000cc"}}},
		"ocr fails":            {cover: ScreenCover{Find: "x"}, setup: func(m *mockAdapter) { m.ocrErr = errors.New("vision failed") }},
		"bad colour":           {cover: ScreenCover{Rect: &Rect{W: 10, H: 10}, Style: CoverStyle{Color: "nope"}}},
		"empty rect":           {cover: ScreenCover{Rect: &Rect{W: 0, H: 10}}},
		"no target":            {cover: ScreenCover{}},
		"rect and window both": {cover: ScreenCover{Rect: &Rect{W: 1, H: 1}, Match: &Match{App: "Preview"}}},
	} {
		m := shotMock(1)
		if tc.setup != nil {
			tc.setup(m)
		}
		dir := t.TempDir()
		_, err := screenshotCoveredWith(m, ScreenshotOptions{OutPath: filepath.Join(dir, "s.png"), Covers: []ScreenCover{tc.cover}})
		if err == nil {
			t.Errorf("%s: captured", name)
			continue
		}
		if tc.notHere != errors.Is(err, ErrNoMatch) {
			t.Errorf("%s: ErrNoMatch = %v, err %v", name, errors.Is(err, ErrNoMatch), err)
		}
		if name == "tint" && !errors.Is(err, ErrCoverTint) {
			t.Errorf("tint error = %v", err)
		}
		if m.capturedPath != "" {
			t.Errorf("%s: captured before every cover was placed", name)
		}
		noFilesLeft(t, dir)
	}
}

func TestScreenshotCoverPermissionDeniedWritesNothing(t *testing.T) {
	m := shotMock(1)
	m.captureErr = ErrScreenCaptureDenied
	dir := t.TempDir()
	_, err := screenshotCoveredWith(m, ScreenshotOptions{OutPath: filepath.Join(dir, "s.png"),
		Covers: []ScreenCover{{Rect: &Rect{W: 10, H: 10}}}})
	if !errors.Is(err, ErrScreenCaptureDenied) {
		t.Fatalf("err = %v", err)
	}
	noFilesLeft(t, dir)
}

// A capture that is not a PNG (a broken adapter) is refused, not copied.
func TestScreenshotCoverUnreadableCaptureWritesNothing(t *testing.T) {
	m := shotMock(1)
	m.captureImg = nil // the mock "captures" nothing
	dir := t.TempDir()
	if _, err := screenshotCoveredWith(m, ScreenshotOptions{OutPath: filepath.Join(dir, "s.png"),
		Covers: []ScreenCover{{Rect: &Rect{W: 10, H: 10}}}}); err == nil {
		t.Fatal("wrote a file from a capture it could not read")
	}
	noFilesLeft(t, dir)
}
