package engine

import (
	"testing"

	"github.com/muthuishere/aos/internal/engine/core"
)

func TestReadScreenTextRegionOnMonitor(t *testing.T) {
	a := &mockAdapter{
		monitors: []Monitor{
			{ID: 1, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: 2, X: 1920, Y: 0, Width: 1440, Height: 900},
		},
		ocrTexts: []core.TextItem{
			{Text: "second line", Bounds: Rect{X: 1950, Y: 140, W: 200, H: 20}},
			{Text: "first line", Bounds: Rect{X: 1950, Y: 110, W: 200, H: 20}},
			{Text: "outside", Bounds: Rect{X: 500, Y: 500, W: 50, H: 20}},
		},
	}
	two := 2
	got, r, err := readScreenTextWith(a, ScreenTextOptions{Region: &Rect{X: 20, Y: 100, W: 400, H: 100}, Monitor: &two})
	if err != nil {
		t.Fatal(err)
	}
	if r != (Rect{X: 1940, Y: 100, W: 400, H: 100}) || a.ocrRect != r {
		t.Errorf("region relative to monitor 2 = %+v (ocr saw %+v)", r, a.ocrRect)
	}
	if got.Text != "first line\nsecond line" || got.Source != "ocr" {
		t.Errorf("text = %q source = %q", got.Text, got.Source)
	}
}

func TestReadScreenTextWholeMonitorAndLimit(t *testing.T) {
	a := &mockAdapter{
		monitors: []Monitor{{ID: 1, X: 0, Y: 0, Width: 800, Height: 600, Primary: true}},
		ocrTexts: []core.TextItem{{Text: "hello world", Bounds: Rect{X: 10, Y: 10, W: 100, H: 20}}},
	}
	got, r, err := readScreenTextWith(a, ScreenTextOptions{MaxChars: 5})
	if err != nil {
		t.Fatal(err)
	}
	if r != (Rect{X: 0, Y: 0, W: 800, H: 600}) || got.Text != "hello" || !got.Truncated {
		t.Errorf("rect %+v text %q truncated %v", r, got.Text, got.Truncated)
	}
}
