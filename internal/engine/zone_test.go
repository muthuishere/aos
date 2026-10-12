package engine

import (
	"strings"
	"testing"
)

func TestParseZoneAcceptsEnumCaseInsensitive(t *testing.T) {
	for _, in := range []string{"1A", "1a", "2D", "2d"} {
		z, err := ParseZone(in)
		if err != nil {
			t.Fatalf("ParseZone(%q): %v", in, err)
		}
		if z == nil {
			t.Fatalf("ParseZone(%q) returned nil", in)
		}
	}
}

func TestParseZoneRejectsInvalidEnum(t *testing.T) {
	for _, in := range []string{"3A", "1C", "foo", ""} {
		if _, err := ParseZone(in); err == nil {
			t.Fatalf("ParseZone(%q) expected error", in)
		}
	}
}

func TestParseZoneAcceptsValidSplits(t *testing.T) {
	for _, in := range []string{"2:1", "2:2", "3:1", "3:2", "3:3", "10:5"} {
		if _, err := ParseZone(in); err != nil {
			t.Fatalf("ParseZone(%q): %v", in, err)
		}
	}
}

func TestParseZoneRejectsInvalidSplits(t *testing.T) {
	// "3:4" is no longer here: with both orderings accepted it is the third of
	// four, which is a real zone.
	for _, in := range []string{"0:1", "3:0", "3:-1", "abc:1", "3:abc", ":", "3:"} {
		if _, err := ParseZone(in); err == nil {
			t.Fatalf("ParseZone(%q) expected error", in)
		}
	}
}

// TestParseSplitAcceptsEitherOrder is the point of accepting both: "the M-th of
// N" and "N parts, the M-th" name the same rectangle, and people say the first.
func TestParseSplitAcceptsEitherOrder(t *testing.T) {
	screen := mon(0, 0, 1200, 900)
	for _, pair := range [][2]string{{"1:2", "2:1"}, {"1:3", "3:1"}, {"2:3", "3:2"}, {"3:10", "10:3"}} {
		spoken, err := ParseZone(pair[0])
		if err != nil {
			t.Fatalf("ParseZone(%q): %v", pair[0], err)
		}
		legacy, err := ParseZone(pair[1])
		if err != nil {
			t.Fatalf("ParseZone(%q): %v", pair[1], err)
		}
		if spoken.Rect(screen) != legacy.Rect(screen) {
			t.Errorf("%s and %s should be the same rectangle: %+v vs %+v",
				pair[0], pair[1], spoken.Rect(screen), legacy.Rect(screen))
		}
	}

	// 1:2 is the LEFT half, not the right one.
	left, _ := ParseZone("1:2")
	if r := left.Rect(screen); r.X != 0 || r.W != 600 {
		t.Errorf("1:2 = %+v, want the left half", r)
	}
	right, _ := ParseZone("2:2")
	if r := right.Rect(screen); r.X != 600 {
		t.Errorf("2:2 = %+v, want the right half", r)
	}
}

func mon(x, y, w, h int) Monitor { return Monitor{X: x, Y: y, Width: w, Height: h} }

func TestEnumZoneRectGivesExpectedHalvesAndQuadrants(t *testing.T) {
	m := mon(0, 0, 1920, 1080)

	cases := []struct {
		zone string
		want Rect
	}{
		{"1A", Rect{X: 0, Y: 0, W: 960, H: 1080}},
		{"1B", Rect{X: 960, Y: 0, W: 960, H: 1080}},
		{"2A", Rect{X: 0, Y: 0, W: 960, H: 540}},
		{"2B", Rect{X: 960, Y: 0, W: 960, H: 540}},
		{"2C", Rect{X: 0, Y: 540, W: 960, H: 540}},
		{"2D", Rect{X: 960, Y: 540, W: 960, H: 540}},
	}
	for _, c := range cases {
		z, _ := ParseZone(c.zone)
		got := z.Rect(m)
		if got != c.want {
			t.Errorf("zone %s: got %+v, want %+v", c.zone, got, c.want)
		}
	}
}

func TestEnumZoneRectRespectsMonitorOffset(t *testing.T) {
	m := mon(1920, 0, 1920, 1080)
	z, _ := ParseZone("2B")
	got := z.Rect(m)
	want := Rect{X: 1920 + 960, Y: 0, W: 960, H: 540}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestSplitZoneRectMatchesSpecFormula(t *testing.T) {
	m := mon(0, 0, 1500, 1000)

	cases := []struct {
		zone string
		want Rect
	}{
		{"3:1", Rect{X: 0, Y: 0, W: 500, H: 1000}},
		{"3:2", Rect{X: 500, Y: 0, W: 500, H: 1000}},
		{"3:3", Rect{X: 1000, Y: 0, W: 500, H: 1000}},
	}
	for _, c := range cases {
		z, _ := ParseZone(c.zone)
		got := z.Rect(m)
		if got != c.want {
			t.Errorf("zone %s: got %+v, want %+v", c.zone, got, c.want)
		}
	}
}

func TestSplitZoneRectRespectsMonitorOffset(t *testing.T) {
	m := mon(1920, 100, 1500, 1000)
	z, _ := ParseZone("3:2")
	got := z.Rect(m)
	want := Rect{X: 1920 + 500, Y: 100, W: 500, H: 1000}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// TestEnumZoneRectOnNonPrimaryMonitorIsExactHalves regression-pins
// BUG-7: full-height zones (1A, 1B) on a non-primary monitor must
// resolve to the full Monitor.Height with no menu-bar inset, and
// quarter zones must be pixel-exact halves/quarters of the monitor.
// (The 25px / 60px drift the original bug reported is a macOS AX
// post-move clamp surfaced by the adapter, not a zone.go math error.)
func TestEnumZoneRectOnNonPrimaryMonitorIsExactHalves(t *testing.T) {
	// Non-primary monitor at the right of the primary; the OS gives
	// it no menu bar of its own, so zones must use the full height.
	m := mon(1920, 0, 1920, 1080)
	cases := []struct {
		zone string
		want Rect
	}{
		{"1A", Rect{X: 1920, Y: 0, W: 960, H: 1080}},
		{"1B", Rect{X: 2880, Y: 0, W: 960, H: 1080}},
		{"2A", Rect{X: 1920, Y: 0, W: 960, H: 540}},
		{"2B", Rect{X: 2880, Y: 0, W: 960, H: 540}},
		{"2C", Rect{X: 1920, Y: 540, W: 960, H: 540}},
		{"2D", Rect{X: 2880, Y: 540, W: 960, H: 540}},
	}
	for _, c := range cases {
		z, _ := ParseZone(c.zone)
		got := z.Rect(m)
		if got != c.want {
			t.Errorf("zone %s on non-primary monitor: got %+v, want %+v", c.zone, got, c.want)
		}
	}
}

// TestEnumZoneRectOnOddDimensionsAvoidsRoundingDrift regression-pins
// the right-half / bottom-half "off-by-one" sliver from BUG-7. With
// odd Width / Height, integer division would leave a 1-px gap; the
// right and bottom halves compensate by taking (Width - Width/2)
// rather than Width/2 so the four quadrants tile the monitor with
// no gap.
func TestEnumZoneRectOnOddDimensionsAvoidsRoundingDrift(t *testing.T) {
	m := mon(0, 0, 1921, 1081) // both dims odd
	for _, c := range []struct {
		zone string
		want Rect
	}{
		// halfW = 960 → 1B width must be 961, total = 1921 (no gap).
		{"1A", Rect{X: 0, Y: 0, W: 960, H: 1081}},
		{"1B", Rect{X: 960, Y: 0, W: 961, H: 1081}},
		// halfH = 540 → bottom row height must be 541, total = 1081.
		{"2A", Rect{X: 0, Y: 0, W: 960, H: 540}},
		{"2B", Rect{X: 960, Y: 0, W: 961, H: 540}},
		{"2C", Rect{X: 0, Y: 540, W: 960, H: 541}},
		{"2D", Rect{X: 960, Y: 540, W: 961, H: 541}},
	} {
		z, _ := ParseZone(c.zone)
		got := z.Rect(m)
		if got != c.want {
			t.Errorf("zone %s on odd-dim monitor: got %+v, want %+v", c.zone, got, c.want)
		}
	}
}

// TestEnumZoneRectOnTinyMonitorIsExactHalves regression-pins BUG-8's
// upstream contract: even on a small (1024x640) monitor, the zone
// math must produce exact halves. The OS-side clamp that BUG-8
// targets is detected by the adapter post-move, not by skewing the
// zone math.
func TestEnumZoneRectOnTinyMonitorIsExactHalves(t *testing.T) {
	m := mon(3840, 30, 1024, 640)
	z, _ := ParseZone("1A")
	got := z.Rect(m)
	want := Rect{X: 3840, Y: 30, W: 512, H: 640}
	if got != want {
		t.Fatalf("zone 1A on tiny monitor: got %+v, want %+v", got, want)
	}
}

// TestZoneStringRoundtrip also pins the printed order: a split renders the way
// it is spoken, the M-th of N, which is the form a person types.
func TestZoneStringRoundtrip(t *testing.T) {
	for _, in := range []string{"1A", "2D", "2:3", "5:10"} {
		z, err := ParseZone(in)
		if err != nil {
			t.Fatalf("ParseZone(%q): %v", in, err)
		}
		if !strings.EqualFold(z.String(), in) {
			t.Errorf("Zone.String() = %q, want %q", z.String(), in)
		}
	}
}
