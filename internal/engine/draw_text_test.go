package engine

import (
	"strings"
	"testing"
)

func TestTextPositionsInsideWindow(t *testing.T) {
	a := drawMock() // window w1 at 2000,100 600x400 on monitor 2 (x 1920)
	win := Rect{X: 2000, Y: 100, W: 600, H: 400}
	for p, cell := range textPositions {
		o := DrawOptions{Text: "hello", Style: DrawText, Match: &Match{App: "Mail"}, Position: p, NoConfig: true}
		_, res, err := renderDrawWith(a, o)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		s := res.Sign
		if s.X < win.X || s.Y < win.Y || s.X+s.W > win.X+win.W || s.Y+s.H > win.Y+win.H {
			t.Errorf("%s: label %+v not inside window %+v", p, s, win)
		}
		cx, cy := s.X+s.W/2, s.Y+s.H/2
		wx, wy := win.X+win.W/2, win.Y+win.H/2
		check := func(axis string, c, mid, want int) {
			switch {
			case want < 0 && c >= mid, want > 0 && c <= mid, want == 0 && (c-mid > 2 || mid-c > 2):
				t.Errorf("%s: %s centre %d vs window centre %d, want side %d", p, axis, c, mid, want)
			}
		}
		check("x", cx, wx, cell[0])
		check("y", cy, wy, cell[1])
	}
}

func TestTextDefaultsToBottomCenterSafeArea(t *testing.T) {
	a := drawMock()
	m := 2
	_, res, err := renderDrawWith(a, DrawOptions{Text: "hi", Style: DrawText, Monitor: &m, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "bottom-center" || res.Monitor != 2 {
		t.Fatalf("got %+v", res)
	}
	// 8% of 800 = 64 off the bottom edge.
	if bottom := res.Sign.Y + res.Sign.H; bottom != 800-64 {
		t.Errorf("label bottom %d, want %d", bottom, 800-64)
	}
}

func TestSubtitleFontFollowsContainerHeight(t *testing.T) {
	a := drawMock()
	m1, m2 := 1, 2
	plan := func(m *int) *drawScene {
		sc, err := planDrawWith(a, DrawOptions{Text: "hi", Style: DrawText, Subtitle: true, Monitor: m, NoConfig: true})
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}
	big, small := plan(&m1).text.st.lineH, plan(&m2).text.st.lineH
	if big <= small {
		t.Errorf("1080-high monitor line %.1f should exceed 800-high %.1f", big, small)
	}
}

func TestTextWrapsAndShrinksToTwoLines(t *testing.T) {
	sc, err := planDrawWith(drawMock(), DrawOptions{
		Text: strings.Repeat("a long caption word ", 12), Style: DrawText, Match: &Match{App: "Mail"}, NoConfig: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(sc.text.st.lines); n > 2 && sc.text.st.lineH > 30 {
		t.Errorf("%d lines at line height %.1f: should have shrunk", n, sc.text.st.lineH)
	}
}

func TestTextMarginAndErrors(t *testing.T) {
	a := drawMock()
	m := 2
	_, res, err := renderDrawWith(a, DrawOptions{Text: "hi", Style: DrawText, Monitor: &m, Position: "top-left", Margin: "10", NoConfig: true})
	if err != nil || res.Sign.X != 1930 || res.Sign.Y != 10 {
		t.Errorf("margin 10: %+v %v", res.Sign, err)
	}
	if _, _, err := renderDrawWith(a, DrawOptions{Text: "hi", Style: DrawText, Position: "middle", NoConfig: true}); err == nil {
		t.Error("bad position should fail")
	}
	if _, _, err := renderDrawWith(a, DrawOptions{Text: "hi", Style: DrawText, Background: "nope", NoConfig: true}); err == nil {
		t.Error("bad bg should fail")
	}
}

func TestArrowPositionAliasesFrom(t *testing.T) {
	a := drawMock()
	_, res, err := renderDrawWith(a, DrawOptions{Text: "x", Point: &Point{900, 500}, Position: "bottom-right", NoConfig: true})
	if err != nil || res.From != "bottom-right" {
		t.Fatalf("got %+v %v", res, err)
	}
	if _, _, err := renderDrawWith(a, DrawOptions{Text: "x", Point: &Point{900, 500}, Position: "center", NoConfig: true}); err == nil {
		t.Error("center is text-only")
	}
}
