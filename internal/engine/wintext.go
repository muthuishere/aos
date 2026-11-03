package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// WindowTextOptions selects how ReadWindowText reads one window.
type WindowTextOptions struct {
	// Source: auto (default) reads the accessibility tree and falls back
	// to OCR when it holds no or almost no text (canvas apps, games,
	// remote desktops); accessibility or ocr force one.
	Source FindSource
	// NoRaise skips focusing the window before OCR (it must be visible).
	NoRaise bool
	// MaxChars caps the joined text (0 = no cap); Truncated reports it.
	MaxChars int
	// Timeout bounds the whole read (0 = none).
	Timeout time.Duration
}

// WindowTextItem is one piece of text in a window, in reading order.
type WindowTextItem struct {
	Text   string `json:"text"`
	Value  string `json:"value,omitempty"`
	Role   string `json:"role"`
	Source string `json:"source"`
	Bounds Rect   `json:"bounds"`
}

// WindowText is every piece of text of one window.
type WindowText struct {
	Window    Window           `json:"window"`
	App       string           `json:"app"`
	Source    string           `json:"source"` // ax | uia | atspi | ocr
	Text      string           `json:"text"`
	Truncated bool             `json:"truncated"`
	Items     []WindowTextItem `json:"items"`
}

// minAXChars: below this much accessible text, auto mode reads the
// pixels instead (a window of only title-bar buttons is "no text").
const minAXChars = 24

// ReadWindowText reads all the text of window w.
func ReadWindowText(w Window, o WindowTextOptions) (WindowText, error) {
	return readWindowTextWith(defaultAdapter, w, o)
}

func readWindowTextWith(a Adapter, w Window, o WindowTextOptions) (WindowText, error) {
	if o.Source == "" {
		o.Source = FindAuto
	}
	switch o.Source {
	case FindAuto, FindAccessibility, FindOCR:
	default:
		return WindowText{}, fmt.Errorf("window text: source %q is not one of auto, accessibility, ocr", o.Source)
	}
	if o.Timeout <= 0 {
		return readWindowText(a, w, o)
	}
	type res struct {
		t   WindowText
		err error
	}
	ch := make(chan res, 1)
	go func() { t, err := readWindowText(a, w, o); ch <- res{t, err} }()
	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()
	select {
	case r := <-ch:
		return r.t, r.err
	case <-ctx.Done():
		return WindowText{}, fmt.Errorf("window text: reading %q took longer than %s", w.Title, o.Timeout)
	}
}

func readWindowText(a Adapter, w Window, o WindowTextOptions) (WindowText, error) {
	out := WindowText{Window: w, App: w.App}
	var axErr error
	if o.Source != FindOCR {
		items, err := a.AccessibleTexts(w.ID)
		axErr = err
		if err == nil {
			out.Items = orderText(mergeText(visibleIn(items, w.Bounds)))
			out.Source = axSourceName()
			if o.Source == FindAccessibility || contentChars(out.Items) >= minAXChars {
				return finish(out, o.MaxChars), nil
			}
		} else if o.Source == FindAccessibility {
			return out, err
		}
	}
	if !o.NoRaise {
		if err := a.Focus(w.ID); err == nil {
			sleepFn(250 * time.Millisecond)
		}
	}
	lines, err := a.RecognizeText(w.Bounds)
	if err != nil {
		if o.Source == FindAuto && axErr == nil && len(out.Items) > 0 {
			return finish(out, o.MaxChars), nil // thin AX text beats none
		}
		if axErr != nil {
			return out, fmt.Errorf("window text: accessibility: %v; ocr: %w", axErr, err)
		}
		if errors.Is(err, ErrNotImplemented) && o.Source == FindAuto {
			return finish(out, o.MaxChars), nil
		}
		return out, err
	}
	for i := range lines {
		lines[i].Source, lines[i].Role = "ocr", "text"
	}
	out.Items = orderText(mergeText(visibleIn(lines, w.Bounds)))
	out.Source = "ocr"
	return finish(out, o.MaxChars), nil
}

// visibleIn keeps items with a real box that overlaps the window: the
// menu bar and content scrolled out of view are not "in" the window.
func visibleIn(items []TextItem, wb Rect) []WindowTextItem {
	out := make([]WindowTextItem, 0, len(items))
	for _, it := range items {
		b := it.Bounds
		t := strings.TrimSpace(bidiMarks.Replace(it.Text))
		if b.W <= 0 || b.H <= 0 || t == "" || windowChrome[strings.ToLower(t)] {
			continue
		}
		if wb.W > 0 && !(b.X < wb.X+wb.W && b.X+b.W > wb.X && b.Y < wb.Y+wb.H && b.Y+b.H > wb.Y) {
			continue
		}
		out = append(out, WindowTextItem{Text: t, Value: strings.TrimSpace(bidiMarks.Replace(it.Value)),
			Role: it.Role, Source: it.Source, Bounds: b})
	}
	return out
}

// mergeText drops the same text reported twice: a link and the static
// text inside it, a group named after its only label. Two items are the
// same when their text matches and one box holds the other's center.
// A container whose text is exactly the run of its children is dropped
// in favour of the children, which carry the finer boxes.
func mergeText(items []WindowTextItem) []WindowTextItem {
	out := make([]WindowTextItem, 0, len(items))
	for _, it := range items {
		dup := false
		for j, p := range out {
			if norm(p.Text) == norm(it.Text) && norm(p.Value) == norm(it.Value) &&
				(holdsCenter(p.Bounds, it.Bounds) || holdsCenter(it.Bounds, p.Bounds)) {
				if area(it.Bounds) < area(p.Bounds) {
					out[j] = it // keep the tighter box
				}
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, it)
		}
	}
	// Containers that only repeat their children.
	keep := make([]bool, len(out))
	for i := range out {
		keep[i] = true
	}
	for i, c := range out {
		if c.Value != "" {
			continue
		}
		var kids []string
		for j, k := range out {
			if j != i && area(k.Bounds) < area(c.Bounds) && holdsCenter(c.Bounds, k.Bounds) {
				kids = append(kids, k.Text)
			}
		}
		if len(kids) > 1 && norm(strings.Join(kids, " ")) == norm(c.Text) {
			keep[i] = false
		}
	}
	res := out[:0]
	for i, it := range out {
		if keep[i] {
			res = append(res, it)
		}
	}
	return res
}

// windowChrome: the labels macOS synthesizes for a window's own title-bar
// buttons. They are on every window and are not its text.
var windowChrome = map[string]bool{"close button": true, "minimise button": true,
	"minimize button": true, "full screen button": true, "zoom button": true}

func holdsCenter(outer, inner Rect) bool {
	cx, cy := inner.X+inner.W/2, inner.Y+inner.H/2
	return cx >= outer.X && cx <= outer.X+outer.W && cy >= outer.Y && cy <= outer.Y+outer.H
}

func area(r Rect) int { return r.W * r.H }

// orderText sorts into reading order: rows top to bottom, each row left
// to right. An item joins the current row when it overlaps the row's
// shortest item vertically by at least half of the smaller height, so a
// tall text area beside a line of labels joins that line, not every
// line it spans.
func orderText(items []WindowTextItem) []WindowTextItem {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Bounds.Y < items[j].Bounds.Y })
	var rows [][]WindowTextItem
	var band Rect // the shortest item of the current row: the row's line
	for _, it := range items {
		if n := len(rows); n > 0 {
			a, b := band, it.Bounds
			overlap := min(a.Y+a.H, b.Y+b.H) - max(a.Y, b.Y)
			if overlap*2 >= min(a.H, b.H, 80) {
				rows[n-1] = append(rows[n-1], it)
				if b.H < band.H {
					band = b
				}
				continue
			}
		}
		rows = append(rows, []WindowTextItem{it})
		band = it.Bounds
	}
	out := items[:0:0]
	for _, r := range rows {
		sort.SliceStable(r, func(i, j int) bool { return r[i].Bounds.X < r[j].Bounds.X })
		out = append(out, r...)
	}
	return out
}

// contentChars counts text that is window content, not chrome: window
// buttons ("close button") and the title alone don't make a readable window.
func contentChars(items []WindowTextItem) int {
	n := 0
	for _, it := range items {
		if it.Role == "button" || it.Role == "menuitem" {
			continue
		}
		n += len(it.Text) + len(it.Value)
	}
	return n
}

// Line is how one item prints as plain text: a labelled value reads
// "label: value", everything else is just its text.
func (it WindowTextItem) Line() string {
	if it.Value == "" {
		return it.Text
	}
	if strings.Contains(it.Value, "\n") || len(it.Text) > 60 {
		return it.Text + "\n" + it.Value
	}
	return it.Text + ": " + it.Value
}

func finish(t WindowText, maxChars int) WindowText {
	if t.Items == nil {
		t.Items = []WindowTextItem{}
	}
	lines := make([]string, 0, len(t.Items))
	for _, it := range t.Items {
		lines = append(lines, it.Line())
	}
	t.Text = strings.Join(lines, "\n")
	if maxChars > 0 {
		if r := []rune(t.Text); len(r) > maxChars {
			t.Text, t.Truncated = string(r[:maxChars]), true
		}
	}
	return t
}
