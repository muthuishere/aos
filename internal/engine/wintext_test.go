package engine

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func ti(text, role string, x, y, w, h int) TextItem {
	return TextItem{Text: text, Role: role, Source: "accessibility", Bounds: Rect{X: x, Y: y, W: w, H: h}}
}

func texts(items []WindowTextItem) []string {
	out := []string{}
	for _, it := range items {
		out = append(out, it.Text)
	}
	return out
}

func TestOrderTextRowsThenColumns(t *testing.T) {
	in := visibleIn([]TextItem{
		ti("right", "text", 300, 12, 50, 20),
		ti("second row", "text", 10, 60, 80, 20),
		ti("left", "text", 10, 10, 50, 20),
		ti("tall field", "textfield", 150, 5, 100, 200), // starts above "left" but sits beside it
	}, Rect{})
	got := texts(orderText(in))
	want := []string{"left", "tall field", "right", "second row"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %q, want %q", got, want)
	}
}

func TestMergeTextDropsNestedDuplicatesAndContainers(t *testing.T) {
	in := visibleIn([]TextItem{
		ti("Home", "link", 0, 0, 100, 30),
		ti("Home", "text", 5, 5, 40, 20), // the static text inside the link
		ti("Hello world", "other", 0, 100, 200, 30),
		ti("Hello", "text", 0, 100, 60, 30),
		ti("world", "text", 70, 100, 60, 30),
		ti("Home", "text", 500, 500, 40, 20), // same text elsewhere: kept
	}, Rect{})
	got := texts(mergeText(in))
	want := []string{"Home", "Hello", "world", "Home"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge = %q, want %q", got, want)
	}
	if m := mergeText(in); m[0].Bounds.W != 40 {
		t.Fatalf("kept the loose box %v, want the tighter one", m[0].Bounds)
	}
}

func TestVisibleInDropsChromeOffWindowAndEmpty(t *testing.T) {
	in := []TextItem{
		ti("close button", "button", 10, 10, 10, 10),
		ti("File", "menuitem", 10, -30, 30, 20), // menu bar, above the window
		ti("  ", "text", 20, 20, 10, 10),
		ti("body", "text", 20, 40, 100, 20),
		ti("scrolled away", "text", 20, 5000, 100, 20),
	}
	got := texts(visibleIn(in, Rect{X: 0, Y: 0, W: 800, H: 600}))
	if !reflect.DeepEqual(got, []string{"body"}) {
		t.Fatalf("visible = %q", got)
	}
}

func TestReadWindowTextValuesAndTruncate(t *testing.T) {
	w := Window{ID: "1", App: "Notes", Title: "n", Bounds: Rect{W: 800, H: 600}}
	m := &mockAdapter{windows: []Window{w}, axTexts: map[string][]TextItem{"1": {
		{Text: "To", Value: "bob@example.com", Role: "textfield", Source: "accessibility", Bounds: Rect{X: 10, Y: 10, W: 200, H: 20}},
		ti("A long enough body of text for the accessibility tree.", "text", 10, 50, 300, 20),
	}}}
	got, err := readWindowTextWith(m, w, WindowTextOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != axSourceName() || got.Text != "To: bob@example.com\nA long enough body of text for the accessibility tree." {
		t.Fatalf("got %q from %s", got.Text, got.Source)
	}
	got, _ = readWindowTextWith(m, w, WindowTextOptions{MaxChars: 5})
	if got.Text != "To: b" || !got.Truncated {
		t.Fatalf("truncate: %q %v", got.Text, got.Truncated)
	}
}

func TestReadWindowTextAutoFallsBackToOCR(t *testing.T) {
	old := sleepFn
	sleepFn = func(time.Duration) {}
	defer func() { sleepFn = old }()
	w := Window{ID: "1", App: "Game", Bounds: Rect{W: 800, H: 600}}
	m := &mockAdapter{windows: []Window{w},
		axTexts:  map[string][]TextItem{"1": {ti("Game", "text", 0, 0, 50, 20)}},
		ocrTexts: []TextItem{{Text: "Score 42", Bounds: Rect{X: 10, Y: 10, W: 80, H: 20}}},
	}
	got, err := readWindowTextWith(m, w, WindowTextOptions{})
	if err != nil || got.Source != "ocr" || got.Text != "Score 42" {
		t.Fatalf("auto: %q %s %v", got.Text, got.Source, err)
	}
	got, _ = readWindowTextWith(m, w, WindowTextOptions{Source: FindAccessibility})
	if got.Source != axSourceName() || got.Text != "Game" {
		t.Fatalf("accessibility only: %q %s", got.Text, got.Source)
	}
	m.axErr = errors.New("no ax")
	m.ocrErr = ErrNotImplemented
	if _, err := readWindowTextWith(m, w, WindowTextOptions{}); err == nil {
		t.Fatal("both sources failing must be an error")
	}
	if _, err := readWindowTextWith(m, w, WindowTextOptions{Source: "pixels"}); err == nil {
		t.Fatal("bad source accepted")
	}
}
