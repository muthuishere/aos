//go:build windows

// Command testform is a classic Win32 window for the live checks: a multiline
// EDIT holding "start" and a "ClickMe" button that appends " clicked" to it.
// It stands in for a WinForms app without any PowerShell.
//
//	testform.exe <window title>
package main

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	registerClassW = user32.NewProc("RegisterClassExW")
	createWindowW  = user32.NewProc("CreateWindowExW")
	defWindowProcW = user32.NewProc("DefWindowProcW")
	getMessageW    = user32.NewProc("GetMessageW")
	translateMsg   = user32.NewProc("TranslateMessage")
	dispatchMsgW   = user32.NewProc("DispatchMessageW")
	postQuit       = user32.NewProc("PostQuitMessage")
	showWindow     = user32.NewProc("ShowWindow")
	sendMessageW   = user32.NewProc("SendMessageW")
	moveWindow     = user32.NewProc("MoveWindow")
	loadCursorW    = user32.NewProc("LoadCursorW")
	getModuleW     = kernel32.NewProc("GetModuleHandleW")
)

const (
	wsOverlapped = 0x00CF0000 // WS_OVERLAPPEDWINDOW
	wsVisible    = 0x10000000
	wsChild      = 0x40000000
	wsBorder     = 0x00800000
	wsVScroll    = 0x00200000
	esMultiline  = 0x0004
	esAutoVS     = 0x0040
	wmDestroy    = 0x0002
	wmSize       = 0x0005
	wmCommand    = 0x0111
	emSetSel     = 0x00B1
	emReplaceSel = 0x00C2
	buttonID     = 1001
	buttonHeight = 40
)

type wndClassEx struct {
	Size, Style                uint32
	WndProc                    uintptr
	ClsExtra, WndExtra         int32
	Instance, Icon, Cursor, Bg uintptr
	MenuName, ClassName        *uint16
	IconSm                     uintptr
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

var edit, button uintptr

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

func wndProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case wmCommand:
		if w&0xFFFF == buttonID {
			sendMessageW.Call(edit, emSetSel, ^uintptr(0), ^uintptr(0))
			sendMessageW.Call(edit, emReplaceSel, 0, uintptr(unsafe.Pointer(u16(" clicked"))))
		}
		return 0
	case wmSize:
		width, height := int32(l&0xFFFF), int32((l>>16)&0xFFFF)
		moveWindow.Call(edit, 0, 0, uintptr(width), uintptr(height-buttonHeight), 1)
		moveWindow.Call(button, 0, uintptr(height-buttonHeight), uintptr(width), buttonHeight, 1)
		return 0
	case wmDestroy:
		postQuit.Call(0)
		return 0
	}
	r, _, _ := defWindowProcW.Call(h, uintptr(m), w, l)
	return r
}

func main() {
	runtime.LockOSThread()
	title := "aos-testform"
	if len(os.Args) > 1 {
		title = os.Args[1]
	}
	inst, _, _ := getModuleW.Call(0)
	cursor, _, _ := loadCursorW.Call(0, 32512) // IDC_ARROW
	wc := wndClassEx{WndProc: syscall.NewCallback(wndProc), Instance: inst, Cursor: cursor, Bg: 6, ClassName: u16("AosTestForm")}
	wc.Size = uint32(unsafe.Sizeof(wc))
	registerClassW.Call(uintptr(unsafe.Pointer(&wc)))
	win, _, _ := createWindowW.Call(0, uintptr(unsafe.Pointer(u16("AosTestForm"))), uintptr(unsafe.Pointer(u16(title))),
		wsOverlapped|wsVisible, 200, 200, 500, 300, 0, 0, inst, 0)
	edit, _, _ = createWindowW.Call(0, uintptr(unsafe.Pointer(u16("EDIT"))), uintptr(unsafe.Pointer(u16("start"))),
		wsChild|wsVisible|wsBorder|wsVScroll|esMultiline|esAutoVS, 0, 0, 480, 220, win, 0, inst, 0)
	button, _, _ = createWindowW.Call(0, uintptr(unsafe.Pointer(u16("BUTTON"))), uintptr(unsafe.Pointer(u16("ClickMe"))),
		wsChild|wsVisible, 0, 220, 480, buttonHeight, win, buttonID, inst, 0)
	showWindow.Call(win, 5)
	var m msg
	for {
		r, _, _ := getMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		translateMsg.Call(uintptr(unsafe.Pointer(&m)))
		dispatchMsgW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
