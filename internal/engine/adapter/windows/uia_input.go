//go:build windows

package windows

// Focus-free input through UI Automation, for windows that have no classic
// Win32 text control (Chrome, Edge, Electron apps such as VS Code and
// Teams, UWP / WinUI apps). Posted WM_CHAR never reaches those: Chromium
// reads keys from its own input pipeline and WinUI has no child HWND.
//
// Text goes in by setting the element's value, in this order:
//  1. a classic Win32 edit control: EM_REPLACESEL (inserts at the caret,
//     exactly like typing, undoable);
//  2. UIA ValuePattern.SetValue on the window's editable element
//     (the one with keyboard focus, else the first editable one);
//  3. UIA LegacyIAccessible.SetValue (MSAA) on the same element.
//
// Clicks find the element by its box inside the window's own UIA tree (so
// a window covered by another one still works) and activate it with the
// first pattern it supports: Invoke, Toggle, SelectionItem, ExpandCollapse,
// LegacyIAccessible.DoDefaultAction. None of this moves the pointer or
// changes the foreground window.

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

var procSysAllocString = oleaut32.NewProc("SysAllocString")

const (
	emReplaceSel = 0x00C2

	valuePatternID      = 10002
	expandPatternID     = 10005
	selItemPatternID    = 10010
	togglePatternID     = 10015
	legacyPatternID     = 10018
	valueSetValue       = 3
	valueGetValue       = 4
	valueGetReadOnly    = 5
	togglToggle         = 3
	selItemSelect       = 3
	expandExpand        = 3
	expandCollapse      = 4
	expandGetState      = 5
	legacyDoDefault     = 4
	legacySetValue      = 5
	legacyGetValue      = 8
	elIsKeyboardFocusab = 27
	// IUIAutomationElement::get_CurrentNativeWindowHandle
	elCurrentNativeWindowHandle = 36
)

func bstr(s string) uintptr {
	p, _ := windows.UTF16PtrFromString(s)
	b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(p)))
	return b
}

func freeBSTR(b uintptr) {
	if b != 0 {
		procSysFreeString.Call(b)
	}
}

// uiaSession is one COM apartment + IUIAutomation on a locked thread.
type uiaSession struct{ uia uintptr }

func openUIA() (*uiaSession, func(), error) {
	runtime.LockOSThread()
	uninit := func() {}
	if hr, _, _ := procCoInitializeEx.Call(0, 2); hr == 0 || hr == 1 {
		uninit = func() { procCoUninitialize.Call() }
	}
	var uia uintptr
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, 1|4,
		uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&uia))); failed(hr) || uia == 0 {
		uninit()
		runtime.UnlockOSThread()
		return nil, nil, hrErr("CoCreateInstance(CUIAutomation)", hr)
	}
	return &uiaSession{uia}, func() { release(uia); uninit(); runtime.UnlockOSThread() }, nil
}

// descendants calls each for every element under top (AddRef'd; each
// must not keep it). Bounded at 8000.
func (s *uiaSession) descendants(top uintptr, each func(el uintptr) bool) error {
	var root uintptr
	if hr := vcall(s.uia, uiaElementFromHandle, top, uintptr(unsafe.Pointer(&root))); failed(hr) || root == 0 {
		return fmt.Errorf("%w: the window is not in the UI Automation tree", core.ErrNoMatch)
	}
	defer release(root)
	var cond uintptr
	if hr := vcall(s.uia, uiaCreateTrueCondition, uintptr(unsafe.Pointer(&cond))); failed(hr) {
		return hrErr("CreateTrueCondition", hr)
	}
	defer release(cond)
	var arr uintptr
	if hr := vcall(root, elFindAll, treeScopeDescendants, cond, uintptr(unsafe.Pointer(&arr))); failed(hr) || arr == 0 {
		return hrErr("FindAll", hr)
	}
	defer release(arr)
	var n int32
	vcall(arr, arrLength, uintptr(unsafe.Pointer(&n)))
	for i := int32(0); i < n && i < 8000; i++ {
		var el uintptr
		if hr := vcall(arr, arrGetElement, uintptr(i), uintptr(unsafe.Pointer(&el))); failed(hr) || el == 0 {
			continue
		}
		more := each(el)
		release(el)
		if !more {
			break
		}
	}
	return nil
}

func pattern(el uintptr, id int) uintptr {
	var p uintptr
	if hr := vcall(el, elGetCurPattern, uintptr(id), uintptr(unsafe.Pointer(&p))); failed(hr) {
		return 0
	}
	return p
}

func getBSTR(obj uintptr, slot int) string {
	var b uintptr
	vcall(obj, slot, uintptr(unsafe.Pointer(&b)))
	s := bstrToString(b)
	freeBSTR(b)
	return s
}

// SetTextTo puts text into window w without focusing it and says how.
// The text is inserted at the caret for classic edit controls and
// appended to the field's value for UIA elements.
func (a *Adapter) SetTextTo(w core.Window, text string) (string, error) {
	top, err := parseHwnd(w.ID)
	if err != nil {
		return "", err
	}
	if isConsoleWindow(top) {
		return consoleTypeTo(top, w, text)
	}
	text = strings.ReplaceAll(text, "\n", "\r\n")
	if ctl, err := inputControl(top, true); err == nil {
		p, _ := windows.UTF16PtrFromString(text)
		var res uintptr
		if r, _, _ := procSendMessageTimeout.Call(ctl, emReplaceSel, 1, uintptr(unsafe.Pointer(p)),
			smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&res))); r != 0 {
			return "win32 EM_REPLACESEL into " + className(ctl), nil
		}
	}
	s, done, err := openUIA()
	if err != nil {
		return "", err
	}
	defer done()
	var best, first uintptr
	err = s.descendants(top, func(el uintptr) bool {
		var ct int32
		vcall(el, elCurrentControlType, uintptr(unsafe.Pointer(&ct)))
		if ct != 50004 && ct != 50030 && ct != 50003 { // Edit, Document, ComboBox
			return true
		}
		vp := pattern(el, valuePatternID)
		lp := uintptr(0)
		if vp == 0 {
			lp = pattern(el, legacyPatternID)
			if lp == 0 {
				return true
			}
			release(lp)
		} else {
			var ro int32
			vcall(vp, valueGetReadOnly, uintptr(unsafe.Pointer(&ro)))
			release(vp)
			if ro != 0 {
				return true
			}
		}
		var focus int32
		vcall(el, elHasKeyboardFocus, uintptr(unsafe.Pointer(&focus)))
		if focus != 0 {
			vcall(el, 1) // AddRef: keep it past the callback
			best = el
			return false
		}
		if first == 0 {
			vcall(el, 1)
			first = el
		}
		return true
	})
	if err != nil {
		return "", err
	}
	target := best
	if target == 0 {
		target = first
	} else if first != 0 {
		release(first)
	}
	if target == 0 {
		return "", errors.New("the window has no editable element in its UI Automation tree")
	}
	defer release(target)
	name := getBSTR(target, elCurrentName)
	// An element backed by its own Win32 edit window: message it directly.
	// The UIA proxy's SetValue for Win32 edits calls SetFocus first, which
	// activates the window.
	var h uintptr // UIA_HWND is pointer-sized
	vcall(target, elCurrentNativeWindowHandle, uintptr(unsafe.Pointer(&h)))
	if h != 0 && h != top && isTextControl(h) {
		p, _ := windows.UTF16PtrFromString(text)
		var res uintptr
		if r, _, _ := procSendMessageTimeout.Call(h, emReplaceSel, 1, uintptr(unsafe.Pointer(p)),
			smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&res))); r != 0 {
			return "win32 EM_REPLACESEL into " + className(h), nil
		}
	}
	if vp := pattern(target, valuePatternID); vp != 0 {
		cur := getBSTR(vp, valueGetValue)
		b := bstr(cur + text)
		hr := vcall(vp, valueSetValue, b)
		freeBSTR(b)
		release(vp)
		if !failed(hr) {
			return fmt.Sprintf("UIA ValuePattern.SetValue on %q", name), nil
		}
	}
	if lp := pattern(target, legacyPatternID); lp != 0 {
		cur := getBSTR(lp, legacyGetValue)
		b := bstr(cur + text)
		hr := vcall(lp, legacySetValue, b)
		freeBSTR(b)
		release(lp)
		if !failed(hr) {
			return fmt.Sprintf("UIA LegacyIAccessible.SetValue on %q", name), nil
		}
	}
	return "", fmt.Errorf("the editable element %q refused SetValue", name)
}

// activate runs the first activation pattern el supports. legacyFirst
// tries LegacyIAccessible.DoDefaultAction before Invoke: in a UWP frame
// (Calculator) the first Invoke after launch brings the app to the front,
// DoDefaultAction presses the same button without activating it (measured
// on Windows 11 ARM64, 2026-11-03).
func activate(el uintptr, legacyFirst bool) (string, bool) {
	if legacyFirst {
		if p := pattern(el, legacyPatternID); p != 0 {
			hr := vcall(p, legacyDoDefault)
			release(p)
			if !failed(hr) {
				return "LegacyIAccessible.DoDefaultAction", true
			}
		}
	}
	if p := pattern(el, invokePatternID); p != 0 {
		hr := vcall(p, invokeInvoke)
		release(p)
		if !failed(hr) {
			return "Invoke", true
		}
	}
	if p := pattern(el, togglePatternID); p != 0 {
		hr := vcall(p, togglToggle)
		release(p)
		if !failed(hr) {
			return "Toggle", true
		}
	}
	if p := pattern(el, selItemPatternID); p != 0 {
		hr := vcall(p, selItemSelect)
		release(p)
		if !failed(hr) {
			return "SelectionItem.Select", true
		}
	}
	if p := pattern(el, expandPatternID); p != 0 {
		var st int32
		vcall(p, expandGetState, uintptr(unsafe.Pointer(&st)))
		slot := expandExpand
		if st == 1 { // Expanded
			slot = expandCollapse
		}
		hr := vcall(p, slot)
		release(p)
		if !failed(hr) {
			return "ExpandCollapse", true
		}
	}
	if p := pattern(el, legacyPatternID); p != 0 {
		hr := vcall(p, legacyDoDefault)
		release(p)
		if !failed(hr) {
			return "LegacyIAccessible.DoDefaultAction", true
		}
	}
	return "", false
}

// pressInTree activates the smallest element of window top whose box
// holds (x, y), or its nearest activatable ancestor. It reads the
// window's own tree, so it works when another window covers the point.
func pressInTree(top uintptr, x, y int) error {
	s, done, err := openUIA()
	if err != nil {
		return err
	}
	defer done()
	var hit uintptr
	area := int64(-1)
	err = s.descendants(top, func(el uintptr) bool {
		var r rect
		vcall(el, elCurrentBoundingRect, uintptr(unsafe.Pointer(&r)))
		if int32(x) < r.Left || int32(x) >= r.Right || int32(y) < r.Top || int32(y) >= r.Bottom {
			return true
		}
		a := int64(r.Right-r.Left) * int64(r.Bottom-r.Top)
		if area < 0 || a < area {
			if hit != 0 {
				release(hit)
			}
			vcall(el, 1)
			hit, area = el, a
		}
		return true
	})
	if err != nil {
		return err
	}
	if hit == 0 {
		return fmt.Errorf("no element of the window is at %d,%d", x, y)
	}
	var walker uintptr
	if hr := vcall(s.uia, uiaRawViewWalker, uintptr(unsafe.Pointer(&walker))); failed(hr) || walker == 0 {
		release(hit)
		return hrErr("RawViewWalker", hr)
	}
	defer release(walker)
	el := hit
	for depth := 0; el != 0 && depth < 6; depth++ {
		if _, ok := activate(el, strings.EqualFold(className(top), "ApplicationFrameWindow")); ok {
			release(el)
			return nil
		}
		var parent uintptr
		vcall(walker, walkerGetParent, el, uintptr(unsafe.Pointer(&parent)))
		release(el)
		el = parent
	}
	release(el)
	return fmt.Errorf("the element at %d,%d supports none of Invoke, Toggle, SelectionItem, ExpandCollapse, DoDefaultAction", x, y)
}
