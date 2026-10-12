package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveCurrentMonitorPicksMajorityOverlap(t *testing.T) {
	left := Monitor{ID: 0, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true}
	right := Monitor{ID: 1, X: 1920, Y: 0, Width: 1920, Height: 1080}
	w := Window{Bounds: Rect{X: 1500, Y: 100, W: 1000, H: 800}}

	got, err := resolveCurrentMonitor(w, []Monitor{left, right})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 {
		t.Fatalf("expected the right monitor (more overlap), got monitor %d", got.ID)
	}
}

func TestResolveCurrentMonitorPrefersPrimaryWhenWindowIsOffscreen(t *testing.T) {
	left := Monitor{ID: 0, X: 0, Y: 0, Width: 1920, Height: 1080}
	right := Monitor{ID: 1, X: 1920, Y: 0, Width: 1920, Height: 1080, Primary: true}
	w := Window{Bounds: Rect{X: -5000, Y: -5000, W: 100, H: 100}}

	got, err := resolveCurrentMonitor(w, []Monitor{left, right})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != 1 {
		t.Fatalf("expected primary fallback (id 1), got monitor %d", got.ID)
	}
}

func TestResolveCurrentMonitorErrorsWithoutMonitors(t *testing.T) {
	if _, err := resolveCurrentMonitor(Window{}, nil); err == nil {
		t.Fatal("expected error for empty monitors")
	}
}

func TestFindMonitorReturnsErrorForUnknownID(t *testing.T) {
	ms := []Monitor{{ID: 0}, {ID: 1}}
	if _, err := findMonitor(ms, 9); err == nil {
		t.Fatal("expected error for unknown monitor ID")
	}
}

func TestSortMonitorsOrdersByOriginAndReassignsIDs(t *testing.T) {
	// Mirrors a real 3-display layout where the OS returned them in a
	// non-spatial order: small Sidecar at x=3840 came back as id 1 and
	// the secondary at x=1920 as id 2. Sorted output must be left-to-
	// right with IDs renumbered 0,1,2.
	in := []Monitor{
		{ID: 99, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
		{ID: 99, X: 3840, Y: 30, Width: 1024, Height: 640},
		{ID: 99, X: 1920, Y: 0, Width: 1920, Height: 1080},
	}
	got := sortMonitors(in)
	wantX := []int{0, 1920, 3840}
	wantID := []int{1, 2, 3}
	for i, m := range got {
		if m.ID != wantID[i] {
			t.Fatalf("monitor[%d]: ID = %d, want %d (1-indexed)", i, m.ID, wantID[i])
		}
		if m.X != wantX[i] {
			t.Fatalf("monitor[%d]: X = %d, want %d", i, m.X, wantX[i])
		}
	}
	if !got[0].Primary {
		t.Fatalf("primary flag lost during sort")
	}
}

type movingAdapter struct {
	mockAdapter
}

func newMovingAdapter() *movingAdapter {
	return &movingAdapter{mockAdapter: mockAdapter{
		windows: []Window{{ID: "w1", Title: "Chrome", App: "Google Chrome", Bounds: Rect{X: 100, Y: 100, W: 800, H: 600}}},
		monitors: []Monitor{
			{ID: 0, X: 0, Y: 0, Width: 1920, Height: 1080, Primary: true},
			{ID: 1, X: 1920, Y: 0, Width: 1920, Height: 1080},
		},
	}}
}

func TestMoveZonePlacesWindowOnExplicitMonitor(t *testing.T) {
	a := newMovingAdapter()
	id := 2 // 1-indexed: monitor 2 is the right-side display at X=1920.
	if err := moveZoneWith(a, Match{Title: "chrome"}, &id, "2B"); err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 1920 + 960, Y: 0, W: 960, H: 540}
	if got := a.moved["w1"]; got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMoveZoneAutoResolvesMonitorWhenIDOmitted(t *testing.T) {
	a := newMovingAdapter()
	if err := moveZoneWith(a, Match{Title: "chrome"}, nil, "1A"); err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 0, Y: 0, W: 960, H: 1080}
	if got := a.moved["w1"]; got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMoveZoneErrorsOnInvalidZone(t *testing.T) {
	a := newMovingAdapter()
	err := moveZoneWith(a, Match{Title: "chrome"}, nil, "9X")
	if err == nil || !strings.Contains(err.Error(), "zone") {
		t.Fatalf("expected zone parse error, got %v", err)
	}
}

func TestMoveZoneErrorsOnInvalidMonitor(t *testing.T) {
	a := newMovingAdapter()
	id := 99
	err := moveZoneWith(a, Match{Title: "chrome"}, &id, "1A")
	if err == nil || !strings.Contains(err.Error(), "monitor") {
		t.Fatalf("expected monitor error, got %v", err)
	}
}

func TestMoveZoneErrorsOnNoMatch(t *testing.T) {
	a := newMovingAdapter()
	err := moveZoneWith(a, Match{Title: "nope"}, nil, "1A")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}
