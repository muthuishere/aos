//go:build windows

package windows

// Focus-free input on Windows.
//
// Keystrokes are posted to the target window's focused control (WM_CHAR for
// text, WM_KEYDOWN/UP for keys) instead of being injected with SendInput,
// which always goes to the foreground window — the one the person beside the
// agent is typing in. The control is the target thread's focus window
// (GetGUIThreadInfo) when it belongs to the target, else the first text
// control child (Edit / RichEdit / Notepad's RichEditD2DPT / Scintilla).
// Typed text is read back with WM_GETTEXT, and buttons are pressed with UI
// Automation's Invoke pattern, which moves no pointer.

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

var (
	procGetGUIThreadInfo   = user32.NewProc("GetGUIThreadInfo")
	procIsChild            = user32.NewProc("IsChild")
	procEnumChildWindows   = user32.NewProc("EnumChildWindows")
	procGetClassNameW      = user32.NewProc("GetClassNameW")
	procPostMessageW       = user32.NewProc("PostMessageW")
	procSendMessageTimeout = user32.NewProc("SendMessageTimeoutW")
	procMapVirtualKeyW     = user32.NewProc("MapVirtualKeyW")
	procWindowFromPoint    = user32.NewProc("WindowFromPoint")
	procGetAncestor        = user32.NewProc("GetAncestor")
)

const (
	wmKeyDown        = 0x0100
	wmKeyUp          = 0x0101
	wmChar           = 0x0102
	wmGetText        = 0x000D
	wmGetTextLength  = 0x000E
	smtoAbortIfHung  = 0x0002
	gaRoot           = 2
	uiaElementFromPt = 7
	uiaRawViewWalker = 16
	elGetCurPattern  = 16
	walkerGetParent  = 3
	invokePatternID  = 10000
	invokeInvoke     = 3
)

type guiThreadInfo struct {
	CbSize        uint32
	Flags         uint32
	HwndActive    uintptr
	HwndFocus     uintptr
	HwndCapture   uintptr
	HwndMenuOwner uintptr
	HwndMoveSize  uintptr
	HwndCaret     uintptr
	RcCaret       rect
}

// textControlClasses are window classes that take WM_CHAR and answer
// WM_GETTEXT.
var textControlClasses = []string{"RichEditD2DPT", "Edit", "RichEdit20W", "RichEdit50W", "RICHEDIT60W", "Scintilla"}

func className(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

func isTextControl(h uintptr) bool {
	c := className(h)
	for _, t := range textControlClasses {
		if strings.EqualFold(c, t) {
			return true
		}
	}
	return false
}

// inputControl finds the control inside top that keyboard input should go
// to. needText requires a text control (for typing / reading back).
func inputControl(top uintptr, needText bool) (uintptr, error) {
	tid, _, _ := procGetWindowThreadPID.Call(top, 0)
	gti := guiThreadInfo{CbSize: uint32(unsafe.Sizeof(guiThreadInfo{}))}
	if ok, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&gti))); ok != 0 && gti.HwndFocus != 0 {
		f := gti.HwndFocus
		mine := f == top
		if !mine {
			c, _, _ := procIsChild.Call(top, f)
			mine = c != 0
		}
		if mine && (!needText || isTextControl(f)) {
			return f, nil
		}
	}
	var found uintptr
	cb := syscall.NewCallback(func(h, _ uintptr) uintptr {
		if isTextControl(h) {
			found = h
			return 0
		}
		return 1
	})
	procEnumChildWindows.Call(top, cb, 0)
	if found != 0 {
		return found, nil
	}
	if needText {
		return 0, errors.New("the window has no text control to post input to")
	}
	return top, nil
}

func post(h uintptr, msg uint32, w, l uintptr) error {
	if r, _, err := procPostMessageW.Call(h, uintptr(msg), w, l); r == 0 {
		return fmt.Errorf("PostMessage: %v", err)
	}
	return nil
}

// postKeyDelay paces posted characters; the target drains its queue on its
// own thread and a burst can arrive out of order in WinUI hosts.
const postKeyDelay = 3 * time.Millisecond

// TypeTextTo posts WM_CHAR for each UTF-16 unit to the window's text control.
func (a *Adapter) TypeTextTo(w core.Window, text string) error {
	top, err := parseHwnd(w.ID)
	if err != nil {
		return err
	}
	ctl, err := inputControl(top, true)
	if err != nil {
		return err
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	for _, u := range windows.StringToUTF16(text) {
		if u == 0 {
			break
		}
		if u == '\n' {
			u = '\r'
		}
		if err := post(ctl, wmChar, uintptr(u), 1); err != nil {
			return err
		}
		time.Sleep(postKeyDelay)
	}
	return nil
}

// extendedKeys need the extended-key bit in lParam.
var extendedKeys = map[string]bool{
	"left": true, "right": true, "up": true, "down": true, "home": true, "end": true,
	"pageup": true, "pagedown": true, "delete": true,
}

// PressChordTo posts WM_KEYDOWN / WM_KEYUP for an unmodified key; the
// target's message loop translates it (enter becomes WM_CHAR '\r').
func (a *Adapter) PressChordTo(w core.Window, chord core.Chord) error {
	if chord.Cmd || chord.Ctrl || chord.Alt || chord.Shift {
		return errors.New("modifier chords cannot be posted (the target reads modifiers from the real keyboard)")
	}
	top, err := parseHwnd(w.ID)
	if err != nil {
		return err
	}
	vk, err := vkForKey(chord.Key)
	if err != nil {
		return err
	}
	ctl, err := inputControl(top, false)
	if err != nil {
		return err
	}
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(vk), 0)
	l := uintptr(1) | scan<<16
	if extendedKeys[chord.Key] {
		l |= 1 << 24
	}
	if err := post(ctl, wmKeyDown, uintptr(vk), l); err != nil {
		return err
	}
	time.Sleep(10 * time.Millisecond)
	return post(ctl, wmKeyUp, uintptr(vk), l|1<<30|1<<31)
}

// FocusedText reads the window's text control with WM_GETTEXT.
func (a *Adapter) FocusedText(w core.Window) (string, bool, error) {
	top, err := parseHwnd(w.ID)
	if err != nil {
		return "", false, err
	}
	ctl, err := inputControl(top, true)
	if err != nil {
		return "", false, nil
	}
	var n uintptr
	if r, _, _ := procSendMessageTimeout.Call(ctl, wmGetTextLength, 0, 0, smtoAbortIfHung, 1000, uintptr(unsafe.Pointer(&n))); r == 0 {
		return "", false, errors.New("WM_GETTEXTLENGTH timed out")
	}
	buf := make([]uint16, n+1)
	var got uintptr
	if r, _, _ := procSendMessageTimeout.Call(ctl, wmGetText, n+1, uintptr(unsafe.Pointer(&buf[0])), smtoAbortIfHung, 1000, uintptr(unsafe.Pointer(&got))); r == 0 {
		return "", false, errors.New("WM_GETTEXT timed out")
	}
	return windows.UTF16ToString(buf), true, nil
}

// PressElementAt invokes the UI Automation element under (x, y) — or its
// nearest invokable ancestor — when that point is on window w.
func (a *Adapter) PressElementAt(w core.Window, x, y int) error {
	top, err := parseHwnd(w.ID)
	if err != nil {
		return err
	}
	pt := uintptr(uint32(int32(x))) | uintptr(uint32(int32(y)))<<32
	hit, _, _ := procWindowFromPoint.Call(pt)
	root, _, _ := procGetAncestor.Call(hit, gaRoot)
	if hit == 0 || root != top {
		return fmt.Errorf("%d,%d is not on window %s (something covers it)", x, y, w.ID)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, 2); hr == 0 || hr == 1 {
		defer procCoUninitialize.Call()
	}
	var uia uintptr
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, 1|4,
		uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&uia))); failed(hr) || uia == 0 {
		return hrErr("CoCreateInstance(CUIAutomation)", hr)
	}
	defer release(uia)
	var el uintptr
	if hr := vcall(uia, uiaElementFromPt, pt, uintptr(unsafe.Pointer(&el))); failed(hr) || el == 0 {
		return hrErr("ElementFromPoint", hr)
	}
	var walker uintptr
	if hr := vcall(uia, uiaRawViewWalker, uintptr(unsafe.Pointer(&walker))); failed(hr) || walker == 0 {
		release(el)
		return hrErr("RawViewWalker", hr)
	}
	defer release(walker)
	for depth := 0; el != 0 && depth < 6; depth++ {
		var pat uintptr
		if hr := vcall(el, elGetCurPattern, invokePatternID, uintptr(unsafe.Pointer(&pat))); !failed(hr) && pat != 0 {
			hr := vcall(pat, invokeInvoke)
			release(pat)
			release(el)
			if failed(hr) {
				return hrErr("Invoke", hr)
			}
			return nil
		}
		var parent uintptr
		vcall(walker, walkerGetParent, el, uintptr(unsafe.Pointer(&parent)))
		release(el)
		el = parent
	}
	release(el)
	return fmt.Errorf("the element at %d,%d has no Invoke pattern", x, y)
}
