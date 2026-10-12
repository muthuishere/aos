package groups

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/engine"
)

// testWindow is a focused window as windowctl would report it, so the event
// shaping can be checked without a desktop.
func testWindow() engine.Window {
	return engine.Window{
		ID:      "w-1",
		App:     "Chrome",
		Title:   "aos — GitHub",
		PID:     4242,
		Monitor: 2,
		Bounds:  engine.Rect{X: 0, Y: 25, W: 1920, H: 1055},
	}
}

func TestTrackerReportsOnlyTransitions(t *testing.T) {
	var seen tracker
	values := []string{"one", "one", "two", "two", "three", "one"}
	want := []bool{false, false, true, false, true, true}

	for i, value := range values {
		if got := seen.changed(value); got != want[i] {
			t.Fatalf("changed(%q) at %d = %v, want %v", value, i, got, want[i])
		}
	}
}

func TestTrackerBaselineIsNotAnEvent(t *testing.T) {
	// The very first sample is whatever was already there, so a `--max=1`
	// watcher must still be waiting after it.
	var seen tracker
	if seen.changed("already on the clipboard") {
		t.Fatal("first sample reported as a change")
	}
}

func TestParseInterval(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 500 * time.Millisecond, false},
		{"500ms", 500 * time.Millisecond, false},
		{"2s", 2 * time.Second, false},
		{"750", 750 * time.Millisecond, false},
		{"soon", 0, true},
	}
	for _, tc := range cases {
		got, err := parseInterval(tc.in, 500*time.Millisecond)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseInterval(%q) accepted a non-duration", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("parseInterval(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}

func TestClipboardEventOmitsContentByDefault(t *testing.T) {
	at := time.Date(2026, 8, 31, 10, 30, 0, 0, time.UTC)
	quiet := clipboardEvent(at, "hunter2", false)
	if quiet.Text != "" {
		t.Fatal("clipboard text leaked without --content")
	}
	if quiet.Length == nil || *quiet.Length != 7 {
		t.Fatalf("length = %v", quiet.Length)
	}
	if quiet.Digest == "" || quiet.Digest == "hunter2" {
		t.Fatalf("digest = %q", quiet.Digest)
	}

	loud := clipboardEvent(at, "hunter2", true)
	if loud.Text != "hunter2" {
		t.Fatalf("--content did not include the text: %q", loud.Text)
	}
	if loud.Digest != quiet.Digest {
		t.Fatal("digest depends on whether content was requested")
	}
}

func TestDigestChangesWithContent(t *testing.T) {
	if digest("a") == digest("b") {
		t.Fatal("digest does not distinguish two clipboards")
	}
	if digest("a") != digest("a") {
		t.Fatal("digest is not stable")
	}
}

func TestEventsShareOneEnvelope(t *testing.T) {
	at := time.Date(2026, 8, 31, 10, 30, 0, 0, time.UTC)
	events := []watchEvent{
		clipboardEvent(at, "note", false),
		windowEvent(at, testWindow()),
	}
	for _, event := range events {
		event.Seq = 3
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(line, &decoded); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"event", "at", "seq"} {
			if _, ok := decoded[field]; !ok {
				t.Fatalf("%s missing from %s", field, line)
			}
		}
		if decoded["at"] != "2026-08-31T10:30:00Z" {
			t.Fatalf("at = %v, want RFC3339", decoded["at"])
		}
		if decoded["seq"] != float64(3) {
			t.Fatalf("seq = %v", decoded["seq"])
		}
	}
}

func TestWindowEventCarriesFocusIdentity(t *testing.T) {
	event := windowEvent(time.Now(), testWindow())
	if event.Event != "window" || event.App != "Chrome" || event.Title != "aos — GitHub" {
		t.Fatalf("event = %+v", event)
	}
	if event.Monitor == nil || *event.Monitor != 2 {
		t.Fatalf("monitor = %v", event.Monitor)
	}
	if event.PID != 4242 || event.WindowID != "w-1" {
		t.Fatalf("event = %+v", event)
	}
}

func TestFocusKeyNoticesATitleChange(t *testing.T) {
	// A browser tab switch keeps the window id and changes only the title.
	before := testWindow()
	after := testWindow()
	after.Title = "watch — vim"
	if focusKey(before) == focusKey(after) {
		t.Fatal("a title-only change is not seen as a focus change")
	}

	moved := testWindow()
	moved.Monitor = 1
	if focusKey(before) == focusKey(moved) {
		t.Fatal("a monitor change is not seen as a focus change")
	}
	if focusKey(before) != focusKey(testWindow()) {
		t.Fatal("the same window produces two different keys")
	}
}
