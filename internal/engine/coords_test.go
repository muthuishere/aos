package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestMoveCoordsWithoutMonitorTreatsBoundsAsAbsolute(t *testing.T) {
	a := newMovingAdapter()
	if err := moveCoordsWith(a, Match{Title: "chrome"}, nil, Rect{X: 100, Y: 200, W: 800, H: 600}); err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 100, Y: 200, W: 800, H: 600}
	if got := a.moved["w1"]; got != want {
		t.Fatalf("got %+v, want %+v (absolute)", got, want)
	}
}

func TestMoveCoordsWithMonitorTreatsBoundsAsRelative(t *testing.T) {
	a := newMovingAdapter()
	id := 2 // 1-indexed: monitor 2 is the right-side display at X=1920.
	if err := moveCoordsWith(a, Match{Title: "chrome"}, &id, Rect{X: 50, Y: 60, W: 800, H: 600}); err != nil {
		t.Fatal(err)
	}
	want := Rect{X: 1920 + 50, Y: 0 + 60, W: 800, H: 600}
	if got := a.moved["w1"]; got != want {
		t.Fatalf("got %+v, want %+v (monitor-relative)", got, want)
	}
}

func TestMoveCoordsErrorsOnInvalidMonitor(t *testing.T) {
	a := newMovingAdapter()
	id := 99
	err := moveCoordsWith(a, Match{Title: "chrome"}, &id, Rect{W: 800, H: 600})
	if err == nil || !strings.Contains(err.Error(), "monitor") {
		t.Fatalf("expected monitor error, got %v", err)
	}
}

func TestMoveCoordsErrorsOnNoMatch(t *testing.T) {
	a := newMovingAdapter()
	err := moveCoordsWith(a, Match{Title: "nope"}, nil, Rect{W: 100, H: 100})
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch, got %v", err)
	}
}
