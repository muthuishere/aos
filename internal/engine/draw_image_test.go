package engine

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// solidPNG is a w x h opaque PNG of one colour.
func solidPNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const goodSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50" width="100" height="50">
<defs><linearGradient id="g"><stop offset="0" stop-color="#f00"/><stop offset="1" stop-color="#00f"/></linearGradient></defs>
<rect x="0" y="0" width="100" height="50" fill="url(#g)"/></svg>`

func pixelAt(img *image.RGBA, x, y int) color.RGBA { return img.RGBAAt(x, y) }

func TestDrawImagePNGPlacedAndRendered(t *testing.T) {
	a := drawMock()
	m := 2 // 1280x800 at x 1920
	o := DrawOptions{Style: DrawImage, Image: solidPNG(t, 200, 100, color.RGBA{255, 0, 0, 255}),
		Monitor: &m, Position: "top-right", NoConfig: true}
	img, res, err := renderDrawWith(a, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sign.W != 200 || res.Sign.H != 100 || res.Monitor != 2 {
		t.Fatalf("size/monitor: %+v", res)
	}
	// top-right inside the 5% / 8% safe area of the monitor
	if r := res.Sign.X + res.Sign.W - 1920; r != 1280-64 {
		t.Errorf("right edge %d, want %d", r, 1280-64)
	}
	if res.Sign.Y != 64 {
		t.Errorf("top %d, want 64", res.Sign.Y)
	}
	mid := pixelAt(img, res.Sign.X-1920+100, res.Sign.Y+50)
	if mid.R != 255 || mid.A != 255 {
		t.Errorf("picture pixel %v, want opaque red", mid)
	}
	if out := pixelAt(img, 5, 5); out.A != 0 {
		t.Errorf("outside the picture must stay transparent, got %v", out)
	}
}

func TestDrawImageSizeFromWidthKeepsAspect(t *testing.T) {
	a := drawMock()
	m := 1
	o := DrawOptions{Style: DrawImage, Image: solidPNG(t, 400, 200, color.RGBA{0, 0, 255, 255}), Monitor: &m, Width: "50%", NoConfig: true}
	_, res, err := renderDrawWith(a, o)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sign.W != 960 || res.Sign.H != 480 {
		t.Errorf("got %dx%d, want 960x480", res.Sign.W, res.Sign.H)
	}
}

func TestDrawImageFormatSniffedFromContent(t *testing.T) {
	a := drawMock()
	m := 1
	if _, _, err := renderDrawWith(a, DrawOptions{Style: DrawImage, Image: []byte("not an image at all"), Monitor: &m, NoConfig: true}); err == nil ||
		!strings.Contains(err.Error(), "PNG, JPEG, GIF, WebP or SVG") {
		t.Errorf("garbage must be refused with the formats, got %v", err)
	}
	// an SVG is recognised by its content, whatever the file was called
	img, res, err := renderDrawWith(a, DrawOptions{Style: DrawImage, Image: []byte(goodSVG), Monitor: &m, Width: "200", NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sign.W != 200 || res.Sign.H != 100 {
		t.Fatalf("svg size %+v", res.Sign)
	}
	left := pixelAt(img, res.Sign.X+10, res.Sign.Y+50)
	right := pixelAt(img, res.Sign.X+res.Sign.W-10, res.Sign.Y+50)
	if !(left.R > left.B && right.B > right.R) {
		t.Errorf("gradient not rendered: left %v right %v", left, right)
	}
}

func TestDrawImageSVGRefusesWhatItCannotDraw(t *testing.T) {
	a := drawMock()
	m := 1
	for name, src := range map[string]string{
		"text":    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><text x="1" y="5">hi</text></svg>`,
		"filter":  `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><filter id="f"/><rect width="5" height="5"/></svg>`,
		"rounded": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="5" height="5" rx="2"/></svg>`,
	} {
		_, _, err := renderDrawWith(a, DrawOptions{Style: DrawImage, Image: []byte(src), Monitor: &m, NoConfig: true})
		if err == nil || !strings.Contains(err.Error(), "cannot render") {
			t.Errorf("%s: want a clear refusal, got %v", name, err)
		}
	}
}

func TestDrawImageOpacityAndFades(t *testing.T) {
	a := drawMock()
	m := 1
	img, res, err := renderDrawWith(a, DrawOptions{Style: DrawImage, Image: solidPNG(t, 100, 100, color.RGBA{0, 255, 0, 255}),
		Monitor: &m, Opacity: 0.5, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	p := pixelAt(img, res.Sign.X+50, res.Sign.Y+50)
	if p.A < 120 || p.A > 135 {
		t.Errorf("opacity 0.5 gave alpha %d", p.A)
	}
}

func TestDrawBubbleTailTouchesTargetAndSideFallsBack(t *testing.T) {
	a := drawMock()
	m := 1
	// plenty of room above: the bubble sits on top, tail tip is the point
	at := Point{X: 900, Y: 600}
	img, res, err := renderDrawWith(a, DrawOptions{Style: DrawBubble, Text: "Click Save", Point: &at, Monitor: &m, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "top" || res.Tip.X != 900 || res.Tip.Y != 600 {
		t.Fatalf("got %+v", res)
	}
	if res.Sign.Y+res.Sign.H >= 600 {
		t.Errorf("bubble %+v should be above the point", res.Sign)
	}
	// a pixel just above the tip belongs to the tail
	if p := pixelAt(img, 900, 596); p.A == 0 {
		t.Errorf("no tail near the tip: %v", p)
	}
	// target at the very top: no room above, so it falls to the bottom
	top := Point{X: 900, Y: 20}
	_, res, err = renderDrawWith(a, DrawOptions{Style: DrawBubble, Text: "Look here", Point: &top, Monitor: &m, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "bottom" || res.Sign.Y <= 20 {
		t.Errorf("want bottom placement, got %+v", res)
	}
	// an asked-for side wins when it fits
	_, res, err = renderDrawWith(a, DrawOptions{Style: DrawBubble, Text: "Right", Point: &at, Monitor: &m, From: "right", NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "right" || res.Sign.X <= 900 {
		t.Errorf("want right placement, got %+v", res)
	}
}

func TestDrawBubbleOnAWindowPointsAtItsEdge(t *testing.T) {
	a := drawMock() // Mail window 2000,100 600x400 on monitor 2
	_, res, err := renderDrawWith(a, DrawOptions{Style: DrawBubble, Text: "Your inbox", Match: &Match{App: "Mail"}, From: "bottom", NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Tip.Y != 500 || res.Tip.X != 2300 {
		t.Errorf("tip %+v, want bottom-centre of the window (2300,500)", res.Tip)
	}
}
