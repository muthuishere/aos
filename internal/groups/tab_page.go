package groups

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/muthuishere/aos/internal/engine"
)

// A page is a browser tab's web content as accessibility reports it: a flat
// preorder list of nodes with depths. Everything here is pure, so markdown
// rendering, element refs, matching and change detection are tested on fake
// trees.

// pageElement is one element `tab read` gives a ref, and `tab type` /
// `tab click` act on.
type pageElement struct {
	Ref   string `json:"ref"`
	Role  string `json:"role"`
	Label string `json:"label"`
	Value string `json:"value,omitempty"`

	index  int    // preorder index: what the ref encodes
	axRole string // raw role, part of the fingerprint
	fp     string // engine.WebNode.WebLabel(), the other part
	alt    []string
}

// pageRole names a raw accessibility role the way the markdown shows it, ""
// for structure that is not shown.
func pageRole(n engine.WebNode) string {
	switch n.Role {
	case "AXButton", "AXMenuButton":
		return "button"
	case "AXLink":
		return "link"
	case "AXTextArea":
		return "textarea"
	case "AXTextField":
		if n.Subrole == "AXSearchField" {
			return "search"
		}
		if n.Subrole == "AXSecureTextField" {
			return "password"
		}
		return "textbox"
	case "AXComboBox":
		return "combobox"
	case "AXCheckBox":
		return "checkbox"
	case "AXRadioButton":
		return "radio"
	case "AXPopUpButton":
		return "select"
	case "AXHeading":
		return "heading"
	case "AXImage":
		return "image"
	case "AXStaticText":
		return "text"
	case "AXList":
		return "list"
	}
	return ""
}

var pageActionable = map[string]bool{
	"button": true, "link": true, "textarea": true, "textbox": true, "search": true,
	"password": true, "combobox": true, "checkbox": true, "radio": true, "select": true,
}

var pageEditable = map[string]bool{"textarea": true, "textbox": true, "search": true, "password": true, "combobox": true}

func elementRef(index int) string { return "e" + strconv.Itoa(index) }

func parseElementRef(ref string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(ref), "e"))
	if err != nil || n < 0 || !strings.HasPrefix(strings.TrimSpace(ref), "e") {
		return 0, fmt.Errorf("bad element ref %q: expected e<number>, as `aos tab read` prints it", ref)
	}
	return n, nil
}

// innerText is the text of the nodes under nodes[i], for a link or heading
// whose name lives in its children.
func innerText(nodes []engine.WebNode, i int) string {
	var parts []string
	for j := i + 1; j < len(nodes) && nodes[j].Depth > nodes[i].Depth; j++ {
		if t := nodeText(nodes[j]); t != "" && pageRole(nodes[j]) == "text" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " ")
}

// nodeText is a node's own visible text.
func nodeText(n engine.WebNode) string {
	if n.Role == "AXStaticText" {
		if n.Value != "" {
			return strings.TrimSpace(n.Value)
		}
		return strings.TrimSpace(n.Title)
	}
	return strings.TrimSpace(n.WebLabel())
}

// displayLabel is what the markdown shows for nodes[i], and whether it was
// taken from its children (which are then not shown again).
func displayLabel(nodes []engine.WebNode, i int) (string, bool) {
	n := nodes[i]
	if l := strings.TrimSpace(n.Title); l != "" {
		return l, false
	}
	if l := strings.TrimSpace(n.Desc); l != "" {
		return l, false
	}
	if pageEditable[pageRole(n)] {
		return strings.TrimSpace(n.Placeholder), false
	}
	if t := innerText(nodes, i); t != "" {
		return t, true
	}
	return strings.TrimSpace(n.Placeholder), false
}

// pageElements lists the actionable elements with their refs.
func pageElements(nodes []engine.WebNode) []pageElement {
	var out []pageElement
	for i, n := range nodes {
		role := pageRole(n)
		if !pageActionable[role] {
			continue
		}
		label, _ := displayLabel(nodes, i)
		e := pageElement{Ref: elementRef(i), Role: role, Label: label, index: i, axRole: n.Role, fp: n.WebLabel()}
		if pageEditable[role] || role == "select" {
			e.Value = n.Value
		}
		for _, s := range []string{n.Title, n.Desc, n.Placeholder} {
			if s = strings.TrimSpace(s); s != "" {
				e.alt = append(e.alt, s)
			}
		}
		out = append(out, e)
	}
	return out
}

// annotation is how an element reads inline: `[e8] Send (button)`,
// `[e7] Message (textarea, empty)`, `[e7] Message (textarea: "hi")`.
func (e pageElement) annotation(refs bool) string {
	var b strings.Builder
	if refs {
		b.WriteString("[" + e.Ref + "] ")
	}
	label := e.Label
	if label == "" {
		label = "(unlabelled)"
	}
	b.WriteString(label + " (" + e.Role)
	if pageEditable[e.Role] || e.Role == "select" {
		if e.Value == "" {
			b.WriteString(", empty")
		} else if e.Role == "password" {
			b.WriteString(", filled")
		} else {
			b.WriteString(": " + strconv.Quote(clip(e.Value, 120)))
		}
	}
	b.WriteString(")")
	return b.String()
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// renderPage turns the nodes into markdown lines: headings as #, lists as -,
// actionable elements annotated (with refs when refs is true), static text
// as it reads. Runs of inline text in one parent join into one line.
// textOnly is what `watch tab` compares: field contents and annotations are
// left out (typing into a box is not new page text), link and button labels
// stay as plain text.
func renderPage(nodes []engine.WebNode, refs, textOnly bool) []string {
	elems := map[int]pageElement{}
	for _, e := range pageElements(nodes) {
		elems[e.index] = e
	}
	var (
		lines    []string
		cur      strings.Builder
		curDepth = -1
		lists    []int // depths of the open lists
		prefix   string
		skipOver = -1 // skip nodes deeper than this (their text was used)
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			lines = append(lines, prefix+s)
			prefix = ""
		}
		cur.Reset()
		curDepth = -1
	}
	inline := func(depth int, s string) {
		if cur.Len() > 0 && depth != curDepth {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString(" ")
		}
		cur.WriteString(s)
		curDepth = depth
	}
	for i, n := range nodes {
		if skipOver >= 0 {
			if n.Depth > skipOver {
				continue
			}
			skipOver = -1
		}
		for len(lists) > 0 && lists[len(lists)-1] >= n.Depth {
			lists = lists[:len(lists)-1]
		}
		if len(lists) > 0 && lists[len(lists)-1] == n.Depth-1 {
			flush()
			prefix = strings.Repeat("  ", len(lists)-1) + "- "
		}
		role := pageRole(n)
		if role == "" && curDepth >= 0 && n.Depth < curDepth {
			flush() // a new block starts
		}
		switch {
		case role == "list":
			flush()
			lists = append(lists, n.Depth)
		case role == "heading":
			flush()
			label, _ := displayLabel(nodes, i)
			skipOver = n.Depth // its text is the label
			level := n.Level
			if level < 1 || level > 6 {
				level = 2
			}
			if label != "" {
				prefix = ""
				lines = append(lines, strings.Repeat("#", level)+" "+label)
			}
		case pageActionable[role]:
			skipOver = n.Depth // its text is the label
			switch {
			case !textOnly:
				inline(n.Depth, elems[i].annotation(refs))
			case !pageEditable[role] && elems[i].Label != "":
				inline(n.Depth, elems[i].Label)
			}
		case role == "image":
			if l := nodeText(n); l != "" {
				inline(n.Depth, "!["+l+"](image)")
			}
		case role == "text":
			if t := nodeText(n); t != "" {
				inline(n.Depth, t)
			}
		}
	}
	flush()
	return lines
}

// watchLines is the page text `watch tab` compares: plain lines, without
// heading and list markers.
func watchLines(nodes []engine.WebNode) []string {
	lines := renderPage(nodes, false, true)
	for i, l := range lines {
		l = strings.TrimLeft(l, " ")
		l = strings.TrimPrefix(l, "- ")
		lines[i] = strings.TrimSpace(strings.TrimLeft(l, "#"))
	}
	return lines
}

// matchElements finds the elements whose label (or title, description,
// placeholder) matches q: case-insensitive equality with exact, else a
// substring. Exact label matches win over substring ones.
func matchElements(elems []pageElement, q string, exact bool, keep func(pageElement) bool) []pageElement {
	q = strings.ToLower(strings.TrimSpace(q))
	var full, part []pageElement
	for _, e := range elems {
		if keep != nil && !keep(e) {
			continue
		}
		names := append([]string{e.Label}, e.alt...)
		isFull, isPart := false, false
		for _, s := range names {
			s = strings.ToLower(s)
			if s == q {
				isFull = true
			} else if strings.Contains(s, q) {
				isPart = true
			}
		}
		switch {
		case isFull:
			full = append(full, e)
		case isPart && !exact:
			part = append(part, e)
		}
	}
	if len(full) > 0 {
		return full
	}
	return part
}

// textKey is what a line is compared by between two reads: case, spacing
// and digits folded, so a ticking clock or a "3 minutes ago" is not news.
func textKey(s string) string {
	var b strings.Builder
	lastDigit, lastSpace := false, false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsDigit(r):
			if !lastDigit {
				b.WriteRune('#')
			}
			lastDigit, lastSpace = true, false
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteRune(' ')
			}
			lastDigit, lastSpace = false, true
		default:
			b.WriteRune(r)
			lastDigit, lastSpace = false, false
		}
	}
	return strings.TrimSpace(b.String())
}

// addedText is the text in cur that prev did not have, in cur's order.
// Lines are counted, not set-compared: a reordered page adds nothing, and a
// second "ok" after an earlier "ok" is still news. When a line occurs more
// often now, the later occurrences are the added ones.
func addedText(prev, cur []string) []string {
	have := map[string]int{}
	for _, l := range prev {
		have[textKey(l)]++
	}
	total := map[string]int{}
	for _, l := range cur {
		total[textKey(l)]++
	}
	seen := map[string]int{}
	var out []string
	for _, l := range cur {
		k := textKey(l)
		if k == "" {
			continue
		}
		seen[k]++
		if seen[k] > have[k] && total[k] > have[k] {
			out = append(out, l)
		}
	}
	return out
}
