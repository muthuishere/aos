package ocr

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// testdata/sample.png is a synthetic image: two lines of black Helvetica on
// white, "Invoice 4271 paid" above "Total due: 318.50 EUR".
func TestRecognizeFileSyntheticPNG(t *testing.T) {
	lines, err := RecognizeFile("testdata/sample.png", Options{})
	if runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("want ErrUnsupported on %s, got %v", runtime.GOOS, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %+v", len(lines), lines)
	}
	if !strings.Contains(lines[0].Text, "Invoice 4271") || !strings.Contains(lines[1].Text, "318.50") {
		t.Fatalf("wrong text or order: %q / %q", lines[0].Text, lines[1].Text)
	}
	for _, l := range lines {
		if l.W <= 0 || l.H <= 0 || l.X < 0 || l.Y < 0 || l.X+l.W > 905 || l.Y+l.H > 265 {
			t.Errorf("line %q box out of the 900x260 image: %+v", l.Text, l)
		}
		if len(l.Words) < 3 {
			t.Errorf("line %q has %d words", l.Text, len(l.Words))
		}
	}
	if lines[0].Y >= lines[1].Y {
		t.Errorf("first line should be above the second: %v vs %v", lines[0].Y, lines[1].Y)
	}
	if runtime.GOOS == "darwin" && (lines[0].Confidence <= 0 || lines[0].Confidence > 1) {
		t.Errorf("confidence %v not in (0,1]", lines[0].Confidence)
	}
	if w := lines[0].Words[0]; w.Text != "Invoice" || w.X > lines[0].X+5 {
		t.Errorf("first word %+v", w)
	}
}

func TestReadingOrder(t *testing.T) {
	lines := []Line{{Text: "b", X: 300, Y: 12, H: 20}, {Text: "c", X: 10, Y: 60, H: 20}, {Text: "a", X: 10, Y: 10, H: 20}}
	readingOrder(lines)
	if got := lines[0].Text + lines[1].Text + lines[2].Text; got != "abc" {
		t.Fatalf("order %q", got)
	}
}
