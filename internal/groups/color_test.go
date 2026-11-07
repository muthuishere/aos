package groups

import (
	"image"
	"image/color"
	"testing"
)

func TestParseXY(t *testing.T) {
	for _, in := range [][]string{{"-1200", "300"}, {"-1200,300"}} {
		x, y, err := parseXY(in)
		if err != nil || x != -1200 || y != 300 {
			t.Errorf("parseXY(%v) = %d %d %v", in, x, y, err)
		}
	}
	for _, bad := range [][]string{{}, {"1"}, {"a", "b"}, {"1", "2", "3"}} {
		if _, _, err := parseXY(bad); err == nil {
			t.Errorf("parseXY(%v) should fail", bad)
		}
	}
}

func TestPixelColor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 0x1E, G: 0x90, B: 0xFF, A: 0xFF})
	got := pixelColor(img, 5, 6)
	if got != (colorInfo{X: 5, Y: 6, Hex: "#1E90FF", R: 0x1E, G: 0x90, B: 0xFF}) {
		t.Errorf("got %+v", got)
	}
}
