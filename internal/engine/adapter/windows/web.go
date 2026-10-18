//go:build windows

package windows

// The web content of a browser window through UI Automation, for
// `aos tab read/type/click` and `aos watch tab`. Chrome and Edge put the
// selected tab's page under a Document element; Chromium builds its
// accessibility tree on demand when a UIA client asks, so the first walk can
// find an empty document and is retried briefly.
//
// Roles are mapped onto the macOS AX names so the tab group renders both
// platforms the same way.

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
)

const (
	uiaCompareElements    = 3
	uiaGetFocusedElement  = 8
	uiaControlViewWalker  = 14
	walkerFirstChild      = 4
	walkerNextSibling     = 6
	elSetFocus            = 3
	elGetCurPropValue     = 10
	elHasKeyboardFocus    = 26
	elCurrentHelpText     = 31
	elCurrentAutomationID = 29
	propValueValue        = 30045
	propHeadingLevel      = 30173
	ctDocument            = 50030
)

var procVariantClear = oleaut32.NewProc("VariantClear")

type variant struct {
	VT       uint16
	_, _, _  uint16
	Val, Pad uintptr
}

func uiaWebRole(ct int32, level int) string {
	switch ct {
	case 50000, 50031:
		return "AXButton"
	case 50005:
		return "AXLink"
	case 50004:
		return "AXTextField"
	case 50003:
		return "AXComboBox"
	case 50002:
		return "AXCheckBox"
	case 50013:
		return "AXRadioButton"
	case 50008:
		return "AXList"
	case 50006:
		return "AXImage"
	case 50030:
		return "AXWebArea"
	case 50020:
		if level > 0 {
			return "AXHeading"
		}
		return "AXStaticText"
	}
	return "AXGroup"
}

type uiaWeb struct {
	uia, walker uintptr
}

func (u *uiaWeb) str(el uintptr, slot int) string {
	var b uintptr
	vcall(el, slot, uintptr(unsafe.Pointer(&b)))
	s := strings.TrimSpace(bstrToString(b))
	if b != 0 {
		procSysFreeString.Call(b)
	}
	return s
}

func (u *uiaWeb) prop(el uintptr, id int) variant {
	var v variant
	vcall(el, elGetCurPropValue, uintptr(id), uintptr(unsafe.Pointer(&v)))
	return v
}

func (u *uiaWeb) node(el uintptr, depth int) (core.WebNode, int32) {
	var ct int32
	vcall(el, elCurrentControlType, uintptr(unsafe.Pointer(&ct)))
	n := core.WebNode{Depth: depth, Title: u.str(el, elCurrentName), Placeholder: u.str(el, elCurrentHelpText),
		DOMID: u.str(el, elCurrentAutomationID)}
	if v := u.prop(el, propHeadingLevel); v.VT == 3 /* VT_I4 */ {
		if l := int(int32(v.Val)) - 80050; l > 0 && l < 10 {
			n.Level = l
		}
	}
	if v := u.prop(el, propValueValue); v.VT == 8 /* VT_BSTR */ {
		n.Value = strings.TrimSpace(bstrToString(v.Val))
		procVariantClear.Call(uintptr(unsafe.Pointer(&v)))
	}
	var focus int32
	vcall(el, elHasKeyboardFocus, uintptr(unsafe.Pointer(&focus)))
	n.Focused = focus != 0
	var r rect
	vcall(el, elCurrentBoundingRect, uintptr(unsafe.Pointer(&r)))
	n.Bounds = core.Rect{X: int(r.Left), Y: int(r.Top), W: int(r.Right - r.Left), H: int(r.Bottom - r.Top)}
	n.Role = uiaWebRole(ct, n.Level)
	return n, ct
}

func (u *uiaWeb) children(el uintptr, each func(uintptr) bool) {
	var c uintptr
	vcall(u.walker, walkerFirstChild, el, uintptr(unsafe.Pointer(&c)))
	for c != 0 {
		if !each(c) {
			release(c)
			return
		}
		var next uintptr
		vcall(u.walker, walkerNextSibling, c, uintptr(unsafe.Pointer(&next)))
		release(c)
		c = next
	}
}

// findDocument returns the first Document element under el (AddRef'd).
func (u *uiaWeb) findDocument(el uintptr, depth int, budget *int) uintptr {
	if depth > 40 || *budget <= 0 {
		return 0
	}
	*budget--
	var hit uintptr
	u.children(el, func(c uintptr) bool {
		var ct int32
		vcall(c, elCurrentControlType, uintptr(unsafe.Pointer(&ct)))
		if ct == ctDocument {
			vcall(c, 1) // AddRef: children() releases c
			hit = c
			return false
		}
		hit = u.findDocument(c, depth+1, budget)
		return hit == 0
	})
	return hit
}

type webWalk struct {
	nodes              []core.WebNode
	maxNodes, maxDepth int
	act, action        int
	role, label        string
	rc                 int     // 1 not reached, 0 done, -1 failed, 2 stale, 3 focus did not land
	held               uintptr // the focused target (AddRef'd), for WebFocus
}

func (u *uiaWeb) walk(el uintptr, depth int, w *webWalk) {
	u.children(el, func(c uintptr) bool {
		if len(w.nodes) >= w.maxNodes || depth > w.maxDepth || (w.act >= 0 && w.rc != 1) {
			return false
		}
		n, _ := u.node(c, depth)
		w.nodes = append(w.nodes, n)
		if len(w.nodes)-1 == w.act {
			w.rc = u.perform(c, n, w)
			return false
		}
		if n.Role != "AXStaticText" && n.Role != "AXImage" {
			u.walk(c, depth+1, w)
		}
		return true
	})
}

func (u *uiaWeb) perform(el uintptr, n core.WebNode, w *webWalk) int {
	if n.Role != w.role || n.WebLabel() != w.label {
		return 2
	}
	if w.action == core.WebFocus {
		// Focus, then prove it; never an Invoke instead (a press is not focus).
		if failed(vcall(el, elSetFocus)) {
			return 3
		}
		for k := 0; k < 10; k++ {
			var f uintptr
			if hr := vcall(u.uia, uiaGetFocusedElement, uintptr(unsafe.Pointer(&f))); !failed(hr) && f != 0 {
				var same int32
				vcall(u.uia, uiaCompareElements, el, f, uintptr(unsafe.Pointer(&same)))
				release(f)
				if same != 0 {
					vcall(el, 1) // AddRef: held past the walk
					w.held = el
					return 0
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		return 3
	}
	var pat uintptr
	if hr := vcall(el, elGetCurPattern, invokePatternID, uintptr(unsafe.Pointer(&pat))); !failed(hr) && pat != 0 {
		hr := vcall(pat, invokeInvoke)
		release(pat)
		if !failed(hr) {
			return 0
		}
	}
	return -1
}

func (a *Adapter) webWalk(win core.Window, w *webWalk) error {
	err := a.webWalkThen(win, w, nil)
	if w.held != 0 {
		release(w.held)
		w.held = 0
	}
	return err
}

// webWalkThen walks, then (on success) runs then on the same COM thread
// before anything is released, so held elements stay usable inside it.
func (a *Adapter) webWalkThen(win core.Window, w *webWalk, then func(u *uiaWeb)) error {
	hwnd, err := parseHwnd(win.ID)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, 2); hr == 0 || hr == 1 {
		defer procCoUninitialize.Call()
	}
	u := &uiaWeb{}
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, 1|4,
		uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&u.uia))); failed(hr) || u.uia == 0 {
		return hrErr("CoCreateInstance(CUIAutomation)", hr)
	}
	defer release(u.uia)
	var root uintptr
	if hr := vcall(u.uia, uiaElementFromHandle, hwnd, uintptr(unsafe.Pointer(&root))); failed(hr) || root == 0 {
		return fmt.Errorf("%w: window %s is not in the UI Automation tree", core.ErrNoMatch, win.ID)
	}
	defer release(root)
	if hr := vcall(u.uia, uiaControlViewWalker, uintptr(unsafe.Pointer(&u.walker))); failed(hr) || u.walker == 0 {
		return hrErr("ControlViewWalker", hr)
	}
	defer release(u.walker)
	// Chromium fills its tree in after the first UIA query: poll up to ~1.5s
	// for a document with content.
	for i := 0; ; i++ {
		budget := 4000
		doc := u.findDocument(root, 0, &budget)
		if doc != 0 {
			w.nodes = w.nodes[:0]
			u.walk(doc, 0, w)
			release(doc)
			if len(w.nodes) > 0 || i >= 10 {
				if then != nil {
					then(u)
				}
				return nil
			}
		} else if i >= 10 {
			return errors.New(win.App + " shows no web content to UI Automation for this window (is the tab still loading?)")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// WebTree reads the selected tab's web content of window w.
func (a *Adapter) WebTree(w core.Window, maxNodes, maxDepth int) ([]core.WebNode, string, error) {
	ww := &webWalk{maxNodes: maxNodes, maxDepth: maxDepth, act: -1, rc: 1}
	err := a.webWalk(w, ww)
	return ww.nodes, w.ID, err
}

// WebAct focuses (SetFocus) or presses (Invoke) the node at preorder index,
// only when it still has role and label.
func (a *Adapter) WebAct(w core.Window, maxNodes, maxDepth, index, action int, role, label string) error {
	ww := &webWalk{maxNodes: maxNodes, maxDepth: maxDepth, act: index, action: action, role: role, label: label, rc: 1}
	if err := a.webWalk(w, ww); err != nil {
		return err
	}
	switch ww.rc {
	case 0:
		return nil
	case 1, 2:
		return core.ErrWebStale
	case 3:
		return core.ErrWebFocus
	}
	return errors.New("the element has no Invoke pattern")
}

type winTarget struct {
	reqs chan chan bool
	done chan struct{}
	once sync.Once
}

func (t *winTarget) StillFocused() (bool, error) {
	r := make(chan bool, 1)
	select {
	case t.reqs <- r:
		return <-r, nil
	case <-t.done:
		return false, nil
	}
}

func (t *winTarget) Release() { t.once.Do(func() { close(t.done) }) }

// WebFocus focuses the node at index, proves it has focus (CompareElements
// with GetFocusedElement) and keeps it on its COM thread, so later checks
// compare against the very same element.
func (a *Adapter) WebFocus(win core.Window, maxNodes, maxDepth, index int, role, label string) (core.WebTarget, error) {
	ww := &webWalk{maxNodes: maxNodes, maxDepth: maxDepth, act: index, action: core.WebFocus, role: role, label: label, rc: 1}
	t := &winTarget{reqs: make(chan chan bool), done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		err := a.webWalkThen(win, ww, func(u *uiaWeb) {
			if ww.rc != 0 || ww.held == 0 {
				return
			}
			defer release(ww.held)
			ready <- nil
			for {
				select {
				case r := <-t.reqs:
					same := false
					var f uintptr
					if hr := vcall(u.uia, uiaGetFocusedElement, uintptr(unsafe.Pointer(&f))); !failed(hr) && f != 0 {
						var s int32
						vcall(u.uia, uiaCompareElements, ww.held, f, uintptr(unsafe.Pointer(&s)))
						release(f)
						same = s != 0
					}
					r <- same
				case <-t.done:
					return
				}
			}
		})
		if ww.rc == 0 && err == nil {
			return // served above
		}
		switch {
		case err != nil:
			ready <- err
		case ww.rc == 1 || ww.rc == 2:
			ready <- core.ErrWebStale
		default:
			ready <- core.ErrWebFocus
		}
	}()
	if err := <-ready; err != nil {
		return nil, err
	}
	return t, nil
}

// WebPrepare: UI Automation needs nothing turned on; Chromium builds its
// tree when asked, which webWalk waits for.
func (a *Adapter) WebPrepare(core.Window) (func(), error) { return func() {}, nil }
