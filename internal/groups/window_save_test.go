package groups

import (
	"encoding/json"
	"testing"

	"github.com/muthuishere/aos/internal/engine"
)

// `window arrange` could apply a layout that nothing could produce: the JSON
// had to be hand-written. `window save` closes that, and the only thing worth
// asserting is the round trip — what save writes is what arrange parses back,
// entry for entry, with no display involved on either side.
func TestLayoutFromRoundTrip(t *testing.T) {
	win := func(app, title string, x, y, w, h, monitor int) engine.Window {
		return engine.Window{
			App: app, Title: title, Monitor: monitor,
			Bounds: engine.Rect{X: x, Y: y, W: w, H: h},
		}
	}
	entry := func(app, title string, x, y, w, h int) engine.BatchEntry {
		return engine.BatchEntry{
			App: app, Title: title,
			X: intPtr(x), Y: intPtr(y), W: intPtr(w), H: intPtr(h),
		}
	}

	cases := []struct {
		name    string
		windows []engine.Window
		want    []engine.BatchEntry
	}{
		{
			name:    "one window becomes one entry with absolute bounds",
			windows: []engine.Window{win("Ghostty", "zsh", 0, 25, 1920, 1055, 1)},
			want:    []engine.BatchEntry{entry("Ghostty", "", 0, 25, 1920, 1055)},
		},
		{
			// The monitor is NOT written: a BatchEntry carrying one has its
			// X/Y read as monitor-relative, which would offset the window by
			// that monitor's origin on the way back.
			name:    "a window on a second monitor keeps its global coordinates",
			windows: []engine.Window{win("Slack", "general", 2560, 100, 800, 600, 2)},
			want:    []engine.BatchEntry{entry("Slack", "", 2560, 100, 800, 600)},
		},
		{
			// Titles drift, so one is only pinned when the app name alone
			// could not tell the two windows apart.
			name: "two windows of one app are told apart by title",
			windows: []engine.Window{
				win("Google Chrome", "Inbox", 0, 0, 900, 700, 1),
				win("Google Chrome", "Docs", 900, 0, 900, 700, 1),
			},
			want: []engine.BatchEntry{
				entry("Google Chrome", "Inbox", 0, 0, 900, 700),
				entry("Google Chrome", "Docs", 900, 0, 900, 700),
			},
		},
		{
			name:    "a window with no app is saved by title",
			windows: []engine.Window{win("", "xterm", 10, 10, 400, 300, 1)},
			want:    []engine.BatchEntry{entry("", "xterm", 10, 10, 400, 300)},
		},
		{
			// Arrange rejects w/h <= 0, so saving one would guarantee a
			// failure on the way back.
			name: "a zero-sized window is dropped rather than saved to fail",
			windows: []engine.Window{
				win("Finder", "Desktop", 0, 0, 0, 0, 1),
				win("Ghostty", "zsh", 5, 5, 100, 100, 1),
			},
			want: []engine.BatchEntry{entry("Ghostty", "", 5, 5, 100, 100)},
		},
		{
			name:    "nothing on screen saves nothing",
			windows: nil,
			want:    []engine.BatchEntry{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := layoutFrom(tc.windows)
			if !sameEntries(got, tc.want) {
				t.Fatalf("layoutFrom = %s; want %s", show(t, got), show(t, tc.want))
			}

			// The point of the test: serialise, then parse it back with the
			// very function `window arrange` uses, and get the same entries.
			data, err := json.MarshalIndent(got, "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			parsed, err := parseLayout(data)
			if err != nil {
				t.Fatalf("parseLayout(%s): %v", data, err)
			}
			if !sameEntries(parsed, tc.want) {
				t.Fatalf("round trip = %s; want %s", show(t, parsed), show(t, tc.want))
			}
		})
	}
}

// sameEntries compares by value, since BatchEntry holds *int coordinates that
// would otherwise compare by address.
func sameEntries(a, b []engine.BatchEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].App != b[i].App || a[i].Title != b[i].Title || a[i].Zone != b[i].Zone {
			return false
		}
		for _, pair := range [][2]*int{
			{a[i].X, b[i].X}, {a[i].Y, b[i].Y}, {a[i].W, b[i].W}, {a[i].H, b[i].H},
			{a[i].Monitor, b[i].Monitor},
		} {
			if (pair[0] == nil) != (pair[1] == nil) {
				return false
			}
			if pair[0] != nil && *pair[0] != *pair[1] {
				return false
			}
		}
	}
	return true
}

func show(t *testing.T, entries []engine.BatchEntry) string {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		return "<unmarshalable>"
	}
	return string(data)
}
