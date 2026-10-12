package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// While a recording runs, an overlay with no place of its own belongs on the
// recording's monitor, not on whichever monitor has focus: the person working
// beside the agent owns focus, and a caption on their screen is both noise for
// them and missing from the video.

// recordingMonitor is the monitor the active recording captures, if any.
func recordingMonitor(c *cli.Ctx) (engine.Monitor, bool) {
	st := activeRecording(c)
	if st == nil {
		return engine.Monitor{}, false
	}
	ms, err := engine.ListMonitors()
	if err != nil {
		return engine.Monitor{}, false
	}
	return monitorForRect(ms, st.Monitor)
}

func monitorForRect(ms []engine.Monitor, r engine.Rect) (engine.Monitor, bool) {
	for _, m := range ms {
		if m.X == r.X && m.Y == r.Y && m.Width == r.W && m.Height == r.H {
			return m, true
		}
	}
	return engine.Monitor{}, false
}

// overlayDefaults applies the recording-monitor rule. Pure, for tests.
//
//   - nothing placed (a caption, `draw text`): the recording's monitor;
//   - --at without --monitor: relative to the recording's monitor;
//   - --mouse: kept when the pointer is on the recording's monitor, else the
//     point aos last clicked there, else the monitor's centre;
//   - --monitor, --app/--title or --rect given: untouched.
func overlayDefaults(o engine.DrawOptions, rec engine.Monitor, cursor engine.Point, last *engine.Point) engine.DrawOptions {
	if o.Monitor != nil || o.Match != nil || o.Region != nil {
		return o
	}
	id := rec.ID
	on := func(p engine.Point) bool {
		return p.X >= rec.X && p.X < rec.X+rec.Width && p.Y >= rec.Y && p.Y < rec.Y+rec.Height
	}
	switch {
	case o.Point != nil:
		o.Monitor = &id
	case o.Mouse:
		if on(cursor) {
			return o
		}
		p := engine.Point{X: rec.Width / 2, Y: rec.Height / 2}
		if last != nil && on(*last) {
			p = engine.Point{X: last.X - rec.X, Y: last.Y - rec.Y}
		}
		o.Mouse, o.Point, o.Monitor = false, &p, &id
	default:
		o.Monitor = &id
	}
	return o
}

// applyRecordingMonitor is overlayDefaults against the live recording; a
// no-op without one.
func applyRecordingMonitor(c *cli.Ctx, o engine.DrawOptions) engine.DrawOptions {
	if o.Monitor != nil || o.Match != nil || o.Region != nil {
		return o
	}
	rec, ok := recordingMonitor(c)
	if !ok {
		return o
	}
	var cursor engine.Point
	if x, y, err := engine.CursorPosition(); err == nil {
		cursor = engine.Point{X: x, Y: y}
	}
	var last *engine.Point
	if t := readVideoTarget(videoDir(c)); t != nil {
		last = t.Point
	}
	return overlayDefaults(o, rec, cursor, last)
}
