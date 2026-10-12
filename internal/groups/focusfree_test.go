package groups

import (
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/engine"
)

func TestKeyRoute(t *testing.T) {
	cases := []struct {
		args    []string
		want    string
		wantErr string
	}{
		{[]string{"hello"}, "frontmost", ""},
		{[]string{"hello", "--app=TextEdit"}, "target", ""},
		{[]string{"hello", "--title=notes", "--verify"}, "target", ""},
		{[]string{"hello", "--app=TextEdit", "--focus"}, "target", ""},
		{[]string{"hello", "--verify"}, "", "need a target window"},
		{[]string{"hello", "--focus"}, "", "need a target window"},
	}
	for _, c := range cases {
		set, err := parseArgs(c.args, "app", "title")
		if err != nil {
			t.Fatal(err)
		}
		got, err := keyRoute(set)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%v: want error %q, got %v", c.args, c.wantErr, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%v: got %q, %v; want %q", c.args, got, err, c.want)
		}
	}
}

func TestClickRoute(t *testing.T) {
	cases := []struct {
		args    []string
		want    string
		wantErr string
	}{
		{nil, "pointer", ""},
		{[]string{"400", "300"}, "pointer", ""},
		{[]string{"--app=Calculator", "--find=7"}, "element", ""},
		{[]string{"--title=Calc", "--find=7", "--focus"}, "element", ""},
		{[]string{"--find=7"}, "", "needs the window"},
		{[]string{"--app=Calculator"}, "", "go with --find"},
		{[]string{"--app=Calculator", "--find=7", "1", "2"}, "", "not both"},
	}
	for _, c := range cases {
		set, err := parseArgs(c.args, "app", "title", "find", "role", "monitor", "button")
		if err != nil {
			t.Fatal(err)
		}
		got, err := clickRoute(set)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%v: want error %q, got %v", c.args, c.wantErr, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%v: got %q, %v; want %q", c.args, got, err, c.want)
		}
	}
}

func TestOverlayDefaultsToRecordingMonitor(t *testing.T) {
	rec := engine.Monitor{ID: 1, X: -1728, Y: 0, Width: 1728, Height: 1117}
	ownerCursor := engine.Point{X: 500, Y: 500} // on monitor 2, where the person works
	two := 2

	// A caption with no place: the recording's monitor.
	o := overlayDefaults(engine.DrawOptions{}, rec, ownerCursor, nil)
	if o.Monitor == nil || *o.Monitor != 1 {
		t.Fatalf("caption monitor = %v", o.Monitor)
	}
	// An explicit --monitor, --app or --rect wins.
	if o := overlayDefaults(engine.DrawOptions{Monitor: &two}, rec, ownerCursor, nil); *o.Monitor != 2 {
		t.Fatal("--monitor overridden")
	}
	if o := overlayDefaults(engine.DrawOptions{Match: &engine.Match{App: "X"}}, rec, ownerCursor, nil); o.Monitor != nil {
		t.Fatal("--app given a monitor")
	}
	if o := overlayDefaults(engine.DrawOptions{Region: &engine.Rect{W: 1, H: 1}}, rec, ownerCursor, nil); o.Monitor != nil {
		t.Fatal("--rect given a monitor")
	}
	// --at becomes relative to the recording's monitor.
	if o := overlayDefaults(engine.DrawOptions{Point: &engine.Point{X: 10, Y: 20}}, rec, ownerCursor, nil); o.Monitor == nil || *o.Monitor != 1 {
		t.Fatal("--at not relative to the recording")
	}
	// --mouse with the owner's pointer elsewhere: last aos point, else centre.
	last := engine.Point{X: -1000, Y: 300}
	o = overlayDefaults(engine.DrawOptions{Mouse: true}, rec, ownerCursor, &last)
	if o.Mouse || o.Point == nil || *o.Point != (engine.Point{X: 728, Y: 300}) || *o.Monitor != 1 {
		t.Fatalf("mouse -> %+v %v", o.Point, o.Mouse)
	}
	o = overlayDefaults(engine.DrawOptions{Mouse: true}, rec, ownerCursor, nil)
	if *o.Point != (engine.Point{X: 864, Y: 558}) {
		t.Fatalf("mouse centre -> %+v", o.Point)
	}
	// --mouse with the pointer on the recording: unchanged.
	if o := overlayDefaults(engine.DrawOptions{Mouse: true}, rec, engine.Point{X: -100, Y: 100}, nil); !o.Mouse || o.Point != nil {
		t.Fatal("mouse on the recording was moved")
	}
}

func TestMonitorForRect(t *testing.T) {
	ms := []engine.Monitor{{ID: 1, X: -1728, Width: 1728, Height: 1117}, {ID: 2, Width: 1920, Height: 1080}}
	m, ok := monitorForRect(ms, engine.Rect{X: 0, Y: 0, W: 1920, H: 1080})
	if !ok || m.ID != 2 {
		t.Fatalf("got %+v %v", m, ok)
	}
	if _, ok := monitorForRect(ms, engine.Rect{W: 5, H: 5}); ok {
		t.Fatal("matched a non-monitor")
	}
}

func TestSmartWindowFollowsTargetNotFocus(t *testing.T) {
	ws := []engine.Window{
		{ID: "owner", App: "Code", Focused: true, Bounds: engine.Rect{X: 100, W: 900, H: 700}},
		{ID: "calc", App: "Calculator", Bounds: engine.Rect{X: -900, W: 230, H: 400}},
	}
	if _, ok := smartWindow(ws, nil); ok {
		t.Fatal("no target must log nothing, not the focused window")
	}
	w, ok := smartWindow(ws, &videoTarget{ID: "calc"})
	if !ok || w.ID != "calc" {
		t.Fatalf("got %+v", w)
	}
	if _, ok := smartWindow(ws, &videoTarget{ID: "gone"}); ok {
		t.Fatal("closed target matched")
	}
}

func TestOnCapture(t *testing.T) {
	capture := engine.Rect{X: -1728, Y: 0, W: 1728, H: 1117}
	if !onCapture("window", engine.Rect{X: -900, Y: 60, W: 230, H: 400}, capture) {
		t.Fatal("window on the capture dropped")
	}
	if onCapture("window", engine.Rect{X: 100, Y: 60, W: 800, H: 600}, capture) {
		t.Fatal("window on another monitor kept")
	}
	if onCapture("window", engine.Rect{X: -900, Y: 60, W: 68, H: 22}, capture) {
		t.Fatal("tooltip kept as a window")
	}
}
