package engine

import (
	"errors"
	"fmt"
	"time"
)

// Poll/clock indirection so wait-loop tests run in microseconds
// instead of real time.
const defaultWaitPollInterval = 250 * time.Millisecond

var (
	nowFn   = time.Now
	sleepFn = time.Sleep
)

func msDuration(ms int) time.Duration { return time.Duration(ms) * time.Millisecond }

// ScreenshotOptions selects what Screenshot captures. At most one
// target may be set:
//
//   - Match: capture the matched window's current bounds (same
//     matching rules as Move/Focus — first match wins).
//   - Region: capture an explicit rect. Interpreted as
//     monitor-relative when Monitor is also set, absolute
//     virtual-desktop coordinates otherwise (same rule as MoveCoords).
//   - Monitor alone: capture that monitor's full bounds.
//   - Nothing set: capture the focused monitor, falling back to the
//     primary monitor, falling back to the first.
//
// OutPath is where the PNG is written and must be non-empty (the CLI
// layer defaults it; library callers choose their own).
type ScreenshotOptions struct {
	Monitor *int
	Region  *Rect
	Match   *Match
	OutPath string
}

// Screenshot captures the selected target to opts.OutPath as a PNG and
// returns the virtual-desktop rect that was captured. The returned
// rect is how callers translate image coordinates back to global click
// coordinates: global = (rect.X + imageX, rect.Y + imageY). The image
// is normalized to point dimensions (1 pixel == 1 point) even on
// HiDPI displays, so that translation needs no scale factor.
func Screenshot(opts ScreenshotOptions) (Rect, error) {
	return screenshotWith(defaultAdapter, opts)
}

func screenshotWith(a Adapter, opts ScreenshotOptions) (Rect, error) {
	if opts.OutPath == "" {
		return Rect{}, errors.New("screenshot: output path is required")
	}
	rect, err := resolveScreenshotRect(a, opts)
	if err != nil {
		return Rect{}, err
	}
	if err := a.CaptureRect(rect, opts.OutPath); err != nil {
		return Rect{}, err
	}
	return rect, nil
}

func resolveScreenshotRect(a Adapter, opts ScreenshotOptions) (Rect, error) {
	if opts.Match != nil && (opts.Region != nil || opts.Monitor != nil) {
		return Rect{}, errors.New("screenshot: window match is mutually exclusive with region/monitor")
	}
	if opts.Match != nil {
		w, err := matchOne(a, *opts.Match)
		if err != nil {
			return Rect{}, err
		}
		return w.Bounds, nil
	}
	if opts.Region != nil {
		r := *opts.Region
		if r.W <= 0 || r.H <= 0 {
			return Rect{}, errors.New("screenshot: region width and height must be > 0")
		}
		if opts.Monitor == nil {
			return r, nil
		}
		monitors, err := listMonitorsWith(a)
		if err != nil {
			return Rect{}, err
		}
		mon, err := findMonitor(monitors, *opts.Monitor)
		if err != nil {
			return Rect{}, err
		}
		return Rect{X: mon.X + r.X, Y: mon.Y + r.Y, W: r.W, H: r.H}, nil
	}
	monitors, err := listMonitorsWith(a)
	if err != nil {
		return Rect{}, err
	}
	if opts.Monitor != nil {
		mon, err := findMonitor(monitors, *opts.Monitor)
		if err != nil {
			return Rect{}, err
		}
		return monitorRect(mon), nil
	}
	if len(monitors) == 0 {
		return Rect{}, errors.New("screenshot: no monitors reported")
	}
	return monitorRect(defaultScreenshotMonitor(monitors)), nil
}

// defaultScreenshotMonitor picks which display "screenshot" with no
// target means: the focused monitor (where the user is working), else
// the primary, else the first by ID.
func defaultScreenshotMonitor(ms []Monitor) Monitor {
	for _, m := range ms {
		if m.Focused {
			return m
		}
	}
	for _, m := range ms {
		if m.Primary {
			return m
		}
	}
	return ms[0]
}

func monitorRect(m Monitor) Rect {
	return Rect{X: m.X, Y: m.Y, W: m.Width, H: m.Height}
}

// WaitForWindowOptions would be overkill: timeout is the only knob.
// waitPollInterval is a var so tests can tighten it.
var waitPollInterval = defaultWaitPollInterval

// WaitForWindow polls the window list until one matches filter or
// timeoutMS milliseconds elapse. On success it returns the first
// matched window with Monitor/Focused stamped like ListWindows. It is
// pure composition over ListWindows — no adapter support needed — and
// exists so "launch then act on the new window" scripts don't each
// reinvent the poll loop.
func WaitForWindow(filter Filter, timeoutMS int) (Window, error) {
	return waitForWindowWith(defaultAdapter, filter, timeoutMS)
}

func waitForWindowWith(a Adapter, filter Filter, timeoutMS int) (Window, error) {
	if filter.Title == "" && filter.App == "" {
		return Window{}, errors.New("wait: title or app filter is required")
	}
	if timeoutMS <= 0 {
		return Window{}, errors.New("wait: timeout must be > 0")
	}
	deadline := nowFn().Add(msDuration(timeoutMS))
	for {
		ws, err := listWindowsWith(a, filter)
		if err != nil {
			return Window{}, err
		}
		if len(ws) > 0 {
			return ws[0], nil
		}
		if !nowFn().Before(deadline) {
			return Window{}, fmt.Errorf("wait: no window matched the filter within %dms", timeoutMS)
		}
		sleepFn(waitPollInterval)
	}
}
