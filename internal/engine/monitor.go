package engine

import (
	"fmt"
	"sort"
)

// sortMonitors normalizes a raw adapter monitor list into the order the
// public API exposes: ascending X, then ascending Y, with IDs re-assigned
// 1..N. The adapters' native enumeration order (CGGetActiveDisplayList
// on darwin, EnumDisplayMonitors on windows) is system-defined and not
// stable across reboots/replugs; sorting by origin gives `--monitor 1/2/3`
// a predictable left-to-right meaning the user can read off
// `windowctl monitors list`. IDs are 1-indexed (not 0) because users
// think "monitor 1, monitor 2" — 0 reads as "no monitor."
func sortMonitors(ms []Monitor) []Monitor {
	sort.SliceStable(ms, func(i, j int) bool {
		if ms[i].X != ms[j].X {
			return ms[i].X < ms[j].X
		}
		return ms[i].Y < ms[j].Y
	})
	for i := range ms {
		ms[i].ID = i + 1
	}
	return ms
}

// resolveCurrentMonitor returns the monitor containing the majority of the
// window's visible area, per FR-MOV-03. When no monitor overlaps the window
// at all (e.g. window is offscreen) the primary monitor is returned, falling
// back to the first listed monitor.
func resolveCurrentMonitor(w Window, monitors []Monitor) (Monitor, error) {
	if len(monitors) == 0 {
		return Monitor{}, fmt.Errorf("monitor: no monitors available")
	}
	best := -1
	var winner Monitor
	for _, m := range monitors {
		area := overlapArea(w.Bounds, m)
		if area > best {
			best = area
			winner = m
		}
	}
	if best > 0 {
		return winner, nil
	}
	for _, m := range monitors {
		if m.Primary {
			return m, nil
		}
	}
	return monitors[0], nil
}

// findMonitor returns the monitor with the given ID or an error if missing.
func findMonitor(ms []Monitor, id int) (Monitor, error) {
	for _, m := range ms {
		if m.ID == id {
			return m, nil
		}
	}
	return Monitor{}, fmt.Errorf("monitor: invalid monitor ID %d", id)
}

// monitorIDForCentroid returns the 1-indexed monitor ID whose bounds
// contain the centroid of r. Returns 0 when the centroid lies outside
// every monitor (off-screen). Mirrors the containment check used by
// the darwin adapter's markActive / markFocused so window->monitor
// stamping uses the same anchor as Monitor.Focused.
func monitorIDForCentroid(r Rect, ms []Monitor) int {
	cx := r.X + r.W/2
	cy := r.Y + r.H/2
	for _, m := range ms {
		if cx >= m.X && cx < m.X+m.Width && cy >= m.Y && cy < m.Y+m.Height {
			return m.ID
		}
	}
	return 0
}

func overlapArea(r Rect, m Monitor) int {
	x1 := max(r.X, m.X)
	y1 := max(r.Y, m.Y)
	x2 := min(r.X+r.W, m.X+m.Width)
	y2 := min(r.Y+r.H, m.Y+m.Height)
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	return (x2 - x1) * (y2 - y1)
}
