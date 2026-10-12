package engine

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/muthuishere/aos/internal/engine/core"
)

// TextItem re-exports core.TextItem.
type TextItem = core.TextItem

// FindSource selects how FindText reads the window.
type FindSource string

const (
	// FindAuto tries the accessibility tree first (exact, from the app
	// itself) and falls back to OCR when nothing there matches.
	FindAuto FindSource = "auto"
	// FindAccessibility reads only the AX (macOS) / UI Automation
	// (Windows) tree: buttons, menus, links, labels with exact bounds.
	FindAccessibility FindSource = "accessibility"
	// FindOCR reads only the pixels (Vision / Windows.Media.Ocr): works
	// on canvas, images, remote desktops, anything visible.
	FindOCR FindSource = "ocr"
)

// FindOptions selects a window and the text to look for in it.
type FindOptions struct {
	Match  Match      // the window to search (required; first match wins)
	Text   string     // case-insensitive; matched against whole labels / OCR lines
	Source FindSource // auto (default), accessibility, ocr
	// Role narrows accessibility matches: button, menuitem, link, tab,
	// checkbox, radio, textfield, text, image. Ignored for OCR.
	Role string
	// Exact requires the whole label to equal Text (case-insensitive).
	Exact bool
	// NoRaise skips focusing the window before OCR (it must be visible).
	NoRaise bool
}

// FoundText is one match, best first, in global coordinates.
type FoundText struct {
	Text   string `json:"Text"`  // the matched text (the query span for an OCR substring)
	Label  string `json:"Label"` // the whole label / OCR line it was found in
	Role   string `json:"Role"`
	Source string `json:"Source"`
	Bounds Rect   `json:"Bounds"`
	Center Point  `json:"Center"`
}

var findRoles = map[string]bool{
	"button": true, "menuitem": true, "link": true, "tab": true, "checkbox": true,
	"radio": true, "textfield": true, "text": true, "image": true,
}

// FindText looks for Text inside the matched window and returns every
// match, best first. Errors wrap ErrNoMatch when the window or the text
// is not found (the latter lists the closest labels it did see).
func FindText(opts FindOptions) ([]FoundText, error) {
	return findTextWith(defaultAdapter, opts)
}

func findTextWith(a Adapter, o FindOptions) ([]FoundText, error) {
	q := strings.TrimSpace(o.Text)
	if q == "" {
		return nil, errors.New("find: the text to find is required")
	}
	if o.Match.Title == "" && o.Match.App == "" {
		return nil, errors.New("find: give the window to search with --title or --app")
	}
	if o.Source == "" {
		o.Source = FindAuto
	}
	switch o.Source {
	case FindAuto, FindAccessibility, FindOCR:
	default:
		return nil, fmt.Errorf("find: source %q is not one of auto, accessibility, ocr", o.Source)
	}
	o.Role = strings.ToLower(o.Role)
	if o.Role != "" && !findRoles[o.Role] {
		return nil, fmt.Errorf("find: role %q is not one of button, menuitem, link, tab, checkbox, radio, textfield, text, image", o.Role)
	}
	w, err := matchOne(a, o.Match)
	if err != nil {
		return nil, err
	}
	var seen []TextItem
	var axErr error
	if o.Source != FindOCR {
		items, err := a.AccessibleTexts(w.ID)
		axErr = err
		if err == nil {
			items = withinWindow(items, w.Bounds)
			items = menuBarOnWindowMonitor(a, items, w.Bounds)
			seen = append(seen, items...)
			if hits := rankMatches(items, q, o); len(hits) > 0 {
				if o.Source == FindAuto {
					hits[0] = refineWithOCR(a, w, hits[0], q, o)
				}
				// A menu-bar item only shows the app's menus while the
				// app is frontmost.
				if b := hits[0].Bounds; !o.NoRaise && hits[0].Role == "menuitem" &&
					(b.Y+b.H <= w.Bounds.Y || b.Y >= w.Bounds.Y+w.Bounds.H) {
					_ = a.Focus(w.ID)
				}
				return hits, nil
			}
		}
		if o.Source == FindAccessibility {
			if err != nil {
				return nil, err
			}
			return nil, notFound(q, w, seen, "accessibility tree")
		}
	}
	if !o.NoRaise {
		if err := a.Focus(w.ID); err == nil {
			sleepFn(250 * time.Millisecond) // let it come to the front before capturing
		}
	}
	lines, err := a.RecognizeText(w.Bounds)
	if err != nil {
		if axErr != nil {
			return nil, fmt.Errorf("find: accessibility: %v; ocr: %w", axErr, err)
		}
		return nil, err
	}
	seen = append(seen, lines...)
	if hits := rankMatches(lines, q, o); len(hits) > 0 {
		return hits, nil
	}
	where := "screen text (OCR)"
	if o.Source == FindAuto {
		where = "accessibility tree or screen text (OCR)"
	}
	return nil, notFound(q, w, seen, where)
}

// refineWithOCR narrows a partial match inside a big element (a text
// area, a web view) to the exact words: the accessibility tree knows
// the element contains the text but not where in it. OCR of just that
// element boxes the span. Anything going wrong keeps the AX hit.
func refineWithOCR(a Adapter, w Window, hit FoundText, q string, o FindOptions) FoundText {
	if norm(hit.Label) == norm(q) || hit.Bounds.H < 48 {
		return hit
	}
	if !o.NoRaise {
		if err := a.Focus(w.ID); err == nil {
			sleepFn(250 * time.Millisecond)
		}
	}
	lines, err := a.RecognizeText(hit.Bounds)
	if err != nil {
		return hit
	}
	if spans := rankMatches(lines, q, FindOptions{}); len(spans) > 0 {
		s := spans[0]
		s.Role, s.Source, s.Label = hit.Role, "accessibility+ocr", hit.Label
		return s
	}
	return hit
}

// withinWindow keeps items that overlap the window (menu bar items are
// kept: they belong to the app and sit above the window).
func withinWindow(items []TextItem, wb Rect) []TextItem {
	out := items[:0]
	for _, it := range items {
		b := it.Bounds
		if b.W <= 0 || b.H <= 0 {
			continue
		}
		overlaps := b.X < wb.X+wb.W && b.X+b.W > wb.X && b.Y < wb.Y+wb.H && b.Y+b.H > wb.Y
		if overlaps || it.Role == "menuitem" {
			out = append(out, it)
		}
	}
	return out
}

// menuBarOnWindowMonitor moves menu-bar items onto the window's
// monitor. macOS draws the menu bar on every display but AX reports one
// copy (on whichever display it considers main), so a window on another
// monitor would get pointed at the wrong screen. Items sitting in some
// other monitor's top band, outside the window, are shifted by the
// difference of the two monitors' origins.
func menuBarOnWindowMonitor(a Adapter, items []TextItem, wb Rect) []TextItem {
	mons, err := a.ListMonitors()
	if err != nil || len(mons) < 2 {
		return items
	}
	cx, cy := wb.X+wb.W/2, wb.Y+wb.H/2
	var home *Monitor
	for i := range mons {
		m := &mons[i]
		if cx >= m.X && cx < m.X+m.Width && cy >= m.Y && cy < m.Y+m.Height {
			home = m
		}
	}
	if home == nil {
		return items
	}
	const band = 40 // menu bar height incl. notch displays
	for i, it := range items {
		b := it.Bounds
		if it.Role != "menuitem" || (b.X < wb.X+wb.W && b.X+b.W > wb.X && b.Y < wb.Y+wb.H && b.Y+b.H > wb.Y) {
			continue
		}
		for _, m := range mons {
			if m.ID == home.ID || b.Y < m.Y || b.Y >= m.Y+band || b.X < m.X || b.X >= m.X+m.Width {
				continue
			}
			items[i].Bounds.X += home.X - m.X
			items[i].Bounds.Y += home.Y - m.Y
			break
		}
	}
	return items
}

// bidiMarks are invisible direction marks some apps put in labels (macOS
// Calculator shows "\u200e42"); a search for "42" must still match.
var bidiMarks = strings.NewReplacer("\u200e", "", "\u200f", "", "\u202a", "", "\u202b", "",
	"\u202c", "", "\u202d", "", "\u202e", "", "\u2066", "", "\u2067", "", "\u2068", "", "\u2069", "")

func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(bidiMarks.Replace(s))), " ")
}

// matchTier: 0 exact, 1 whole-word substring, 2 prefix, 3 substring, -1 none.
func matchTier(label, q string) (int, int) {
	l, n := norm(label), norm(q)
	if l == n {
		return 0, 0
	}
	i := strings.Index(l, n)
	if i < 0 {
		return -1, -1
	}
	before := i == 0 || !isWordRune(rune(l[i-1]))
	afterIdx := i + len(n)
	after := afterIdx == len(l) || !isWordRune(rune(l[afterIdx]))
	switch {
	case before && after:
		return 1, i
	case i == 0:
		return 2, i
	}
	return 3, i
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

var interactive = map[string]int{"button": 0, "menuitem": 0, "link": 0, "tab": 0, "checkbox": 0, "radio": 0, "textfield": 1, "text": 2, "image": 2, "other": 3}

func rankMatches(items []TextItem, q string, o FindOptions) []FoundText {
	type cand struct {
		f    FoundText
		tier int
		role int
	}
	var cs []cand
	for _, it := range items {
		if it.Source == "accessibility" && o.Role != "" && it.Role != o.Role {
			continue
		}
		tier, at := matchTier(it.Text, q)
		if tier < 0 || (o.Exact && tier != 0) {
			continue
		}
		f := FoundText{Text: it.Text, Label: it.Text, Role: it.Role, Source: it.Source, Bounds: it.Bounds}
		if it.Source == "ocr" && tier != 0 {
			if b, span, ok := spanBox(it, q, at); ok {
				f.Bounds, f.Text = b, span
			}
		}
		f.Center = Point{f.Bounds.X + f.Bounds.W/2, f.Bounds.Y + f.Bounds.H/2}
		r, ok := interactive[it.Role]
		if !ok {
			r = 3
		}
		cs = append(cs, cand{f, tier, r})
	}
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.role != b.role {
			return a.role < b.role
		}
		if a.f.Bounds.Y != b.f.Bounds.Y {
			return a.f.Bounds.Y < b.f.Bounds.Y
		}
		return a.f.Bounds.X < b.f.Bounds.X
	})
	// Drop duplicates: the same label reported by nested elements.
	out := make([]FoundText, 0, len(cs))
	for _, c := range cs {
		dup := false
		for _, p := range out {
			if norm(p.Label) == norm(c.f.Label) && p.Bounds == c.f.Bounds {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, c.f)
		}
	}
	return out
}

// spanBox boxes the words of an OCR line that the query covers. at is
// the query's byte offset in the normalized line.
func spanBox(line TextItem, q string, at int) (Rect, string, bool) {
	if len(line.Words) == 0 {
		return Rect{}, "", false
	}
	nq := norm(q)
	pos := 0
	var hit []TextItem
	for _, w := range line.Words {
		nw := norm(w.Text)
		start, end := pos, pos+len(nw)
		if end > at && start < at+len(nq) {
			hit = append(hit, w)
		}
		pos = end + 1 // the single space norm() leaves between words
	}
	if len(hit) == 0 {
		return Rect{}, "", false
	}
	b := hit[0].Bounds
	texts := []string{hit[0].Text}
	for _, w := range hit[1:] {
		x0, y0 := min(b.X, w.Bounds.X), min(b.Y, w.Bounds.Y)
		x1, y1 := max(b.X+b.W, w.Bounds.X+w.Bounds.W), max(b.Y+b.H, w.Bounds.Y+w.Bounds.H)
		b = Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
		texts = append(texts, w.Text)
	}
	return b, strings.Join(texts, " "), true
}

// notFound wraps ErrNoMatch and lists up to 8 of the closest labels
// seen, so the caller can retry with the right spelling.
func notFound(q string, w Window, seen []TextItem, where string) error {
	nq := norm(q)
	type s struct {
		t string
		d int
	}
	var ss []s
	uniq := map[string]bool{}
	for _, it := range seen {
		t := strings.TrimSpace(it.Text)
		if t == "" || uniq[norm(t)] || len(t) > 60 {
			continue
		}
		uniq[norm(t)] = true
		ss = append(ss, s{t, editDistance(nq, norm(t))})
	}
	sort.SliceStable(ss, func(i, j int) bool { return ss[i].d < ss[j].d })
	var near []string
	for i := 0; i < len(ss) && i < 8; i++ {
		near = append(near, fmt.Sprintf("%q", ss[i].t))
	}
	msg := fmt.Sprintf("%q not found in the %s of window %q", q, where, w.Title)
	if len(near) > 0 {
		msg += "; closest: " + strings.Join(near, ", ")
	}
	return fmt.Errorf("%w", findErr{msg})
}

type findErr struct{ msg string }

func (e findErr) Error() string { return e.msg }
func (e findErr) Unwrap() error { return ErrNoMatch }

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(rb)]
}
