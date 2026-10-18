// Package core holds the OS-agnostic types and adapter contract that all
// platform adapters and the public windowctl package depend on. It exists to
// break the import cycle that would otherwise form between the public package
// and the per-OS adapter implementations.
package core

type Window struct {
	ID    string
	Title string
	App   string
	PID   int
	// Monitor is the 1-indexed ID of the monitor whose bounds contain
	// the window's centroid. 0 means "off-screen / on no monitor"
	// (the window's centroid does not lie within any reported display).
	// Stamped by the public listWindowsWith path, not by adapters.
	Monitor int
	// Focused is true when this window is the frontmost on the focused
	// monitor — i.e. the topmost (z-ordered) window whose centroid
	// lies on the monitor flagged Focused. Stamped by the public
	// listWindowsWith path.
	Focused bool
	Bounds  Rect
}

type Monitor struct {
	ID      int  `json:"ID"`
	X       int  `json:"X"`
	Y       int  `json:"Y"`
	Width   int  `json:"Width"`
	Height  int  `json:"Height"`
	Primary bool `json:"Primary"`
	// Active is true when the OS cursor is currently over this
	// monitor. Independent of Focused — the user can mouse over one
	// display while typing into a window on another.
	Active bool `json:"Active"`
	// Focused is true when the frontmost (key) window's centroid
	// falls on this monitor. The "where am I working" signal as
	// opposed to Active's "where is my pointer".
	Focused bool `json:"Focused"`
}

// Rect's JSON tags spell the dimensions out (Width/Height) so the wire
// format matches Monitor's. The Go field names stay terse (W/H) so
// existing callers using the public Rect struct don't break.
type Rect struct {
	X int `json:"X"`
	Y int `json:"Y"`
	W int `json:"Width"`
	H int `json:"Height"`
}

type Filter struct {
	Title string
	App   string
}

type Match struct {
	Title string
	App   string
}

type Target struct {
	Monitor *int
	Bounds  Rect
}

// TextItem is one piece of text found in a window, in global
// coordinates: an accessibility element's label (Source
// "accessibility", Role set) or an OCR line (Source "ocr", Words set
// with each word's own box so a substring can be boxed exactly).
type TextItem struct {
	Text   string
	Role   string // normalized: button, menuitem, link, tab, checkbox, radio, textfield, text, image, other
	Bounds Rect
	Source string
	Words  []TextItem
}

// WebNode is one accessibility element inside a browser tab's web area
// (AXWebArea on macOS), in the order a depth-first walk meets it. Depth is
// relative to the web area: its children are depth 0. Strings are empty
// when the element does not have that attribute.
type WebNode struct {
	Depth       int
	Role        string // raw AX role: AXHeading, AXLink, AXButton, AXTextArea, ...
	Subrole     string
	Title       string
	Desc        string
	Value       string // string values only
	Placeholder string
	Level       int // heading level, 0 when unknown
	Focused     bool
	Bounds      Rect
}

// Web-area actions for WebActor.
const (
	WebPress = 1 // AXPress
	WebFocus = 2 // AXFocused = true, falling back to AXPress
)

// WebLabel is the label WebAct fingerprints a node by: the first non-empty
// of title, description and placeholder. Never the value, which typing
// changes. Adapters must compute it the same way.
func (n WebNode) WebLabel() string {
	switch {
	case n.Title != "":
		return n.Title
	case n.Desc != "":
		return n.Desc
	}
	return n.Placeholder
}
