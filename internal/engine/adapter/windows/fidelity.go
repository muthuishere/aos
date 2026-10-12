//go:build windows

// fidelity.go — the Win32 details that decide whether automation lands where
// it was aimed.
//
// The naive calls (GetWindowRect, IsWindowVisible, SetForegroundWindow) each
// report or do something subtly different from what a caller means, and the
// difference is invisible until a window ends up eight pixels off, a minimised
// window shows up in a list, or a focus call succeeds without focusing
// anything. Everything here exists to close one of those gaps.
package windows

import (
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

const (
	// dwmwaCloaked reports a window the compositor is hiding. UWP keeps
	// suspended apps alive as cloaked windows, and IsWindowVisible says they
	// are visible, so without this check a window list is full of apps the user
	// cannot see.
	dwmwaCloaked = 14
	// dwmwaExtendedFrameBounds is the window's *visible* rectangle.
	// GetWindowRect returns the layout rectangle, which on Windows 10 and 11
	// includes an invisible resize border of roughly 7px per side — the reason
	// a maximised window reports a negative origin.
	dwmwaExtendedFrameBounds = 9

	// gwlExStyle is negative in the Win32 headers; as an argument it travels as
	// a two's-complement uintptr.
	gwlExStyle      = ^uintptr(19) // -20
	wsExToolWindow  = 0x00000080
	swRestore       = 9
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpAsyncWindows = 0x4000

	// dpiPerMonitorAwareV2 stops Windows from lying to this process about
	// coordinates on a scaled display. Without it every rectangle we read and
	// every point we click is silently divided by the scale factor, so
	// automation is correct at 100% and wrong everywhere else.
	dpiPerMonitorAwareV2 = ^uintptr(3) // -4 as an unsigned context handle
)

var (
	dwmapi                    = windows.NewLazySystemDLL("dwmapi.dll")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")

	procIsIconic                      = user32.NewProc("IsIconic")
	procShowWindow                    = user32.NewProc("ShowWindow")
	procSetWindowPos                  = user32.NewProc("SetWindowPos")
	procAttachThreadInput             = user32.NewProc("AttachThreadInput")
	procBringWindowToTop              = user32.NewProc("BringWindowToTop")
	procGetWindowLongPtrW             = user32.NewProc("GetWindowLongPtrW")
	procGetShellWindow                = user32.NewProc("GetShellWindow")
	procGetWindowPlacement            = user32.NewProc("GetWindowPlacement")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")

	kernel32               = windows.NewLazySystemDLL("kernel32.dll")
	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")
)

// init makes this process per-monitor DPI aware before any coordinate is read.
// It has to happen once, early, and cannot be undone — which is why it lives in
// init rather than being left to the caller.
func init() {
	if err := procSetProcessDpiAwarenessContext.Find(); err == nil {
		if ret, _, _ := procSetProcessDpiAwarenessContext.Call(dpiPerMonitorAwareV2); ret != 0 {
			return
		}
	}
	// Windows 8.1 and older have no per-monitor context; system awareness is
	// still far better than none.
	if proc := user32.NewProc("SetProcessDPIAware"); proc.Find() == nil {
		proc.Call()
	}
}

// windowPlacement mirrors Win32 WINDOWPLACEMENT. rcNormalPosition is the
// rectangle a window would occupy if restored, which is the only meaningful
// answer for a minimised window: its live rect is the (-32000,-32000) parking
// spot Windows uses to hide it.
type windowPlacement struct {
	Length           uint32
	Flags            uint32
	ShowCmd          uint32
	PtMinPosition    point
	PtMaxPosition    point
	RcNormalPosition rect
}

// placementBounds returns a minimised window's restored rectangle.
func placementBounds(hwnd uintptr) (core.Rect, bool) {
	if err := procGetWindowPlacement.Find(); err != nil {
		return core.Rect{}, false
	}
	wp := windowPlacement{Length: uint32(unsafe.Sizeof(windowPlacement{}))}
	ret, _, _ := procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&wp)))
	if ret == 0 {
		return core.Rect{}, false
	}
	r := wp.RcNormalPosition
	return core.Rect{
		X: int(r.Left), Y: int(r.Top),
		W: int(r.Right - r.Left), H: int(r.Bottom - r.Top),
	}, true
}

// frameBounds returns the window's visible rectangle, falling back to the
// layout rectangle on the rare window DWM has no frame for.
//
// A minimised window is reported at the rectangle it would be restored to, so
// that it can still be listed, matched, and focused without a nonsense origin
// dragging it onto no monitor at all.
func frameBounds(hwnd uintptr) core.Rect {
	if isMinimised(hwnd) {
		if bounds, ok := placementBounds(hwnd); ok {
			return bounds
		}
	}
	var r rect
	ret, _, _ := procDwmGetWindowAttribute.Call(
		hwnd, dwmwaExtendedFrameBounds,
		uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r))
	if ret != 0 || (r.Right-r.Left) <= 0 {
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	}
	return core.Rect{
		X: int(r.Left), Y: int(r.Top),
		W: int(r.Right - r.Left), H: int(r.Bottom - r.Top),
	}
}

// layoutBounds is what MoveWindow and SetWindowPos actually position.
func layoutBounds(hwnd uintptr) core.Rect {
	var r rect
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	return core.Rect{
		X: int(r.Left), Y: int(r.Top),
		W: int(r.Right - r.Left), H: int(r.Bottom - r.Top),
	}
}

// isCloaked reports a window hidden by the compositor rather than by style.
func isCloaked(hwnd uintptr) bool {
	var cloaked uint32
	ret, _, _ := procDwmGetWindowAttribute.Call(
		hwnd, dwmwaCloaked,
		uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked))
	return ret == 0 && cloaked != 0
}

func isMinimised(hwnd uintptr) bool {
	ret, _, _ := procIsIconic.Call(hwnd)
	return ret != 0
}

// isToolWindow reports a palette or notification window, which is real but is
// not something a user would call "a window" when asking to move one.
func isToolWindow(hwnd uintptr) bool {
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlExStyle)
	return style&wsExToolWindow != 0
}

// listable decides whether a window belongs in ListWindows.
func listable(hwnd uintptr, title string) bool {
	if title == "" || isCloaked(hwnd) || isToolWindow(hwnd) {
		return false
	}
	// The shell's desktop window is always present and never interesting.
	if shell, _, _ := procGetShellWindow.Call(); shell == hwnd {
		return false
	}
	return true
}

// appName resolves a PID to the executable's base name, so `--app=Notepad`
// means the same thing on Windows as it does on macOS. Without it the App
// field is empty and every app-based match fails.
func appName(pid uint32) string {
	if pid == 0 {
		return ""
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(handle)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}
	base := filepath.Base(windows.UTF16ToString(buf[:size]))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// positionFrame places a window so its VISIBLE rectangle lands on `want`.
//
// Callers think in terms of what they can see, but SetWindowPos positions the
// layout rectangle, which is larger by the invisible resize border. Asking for
// (0,0) without this correction leaves the window at (-7,-7).
func positionFrame(hwnd uintptr, want core.Rect) error {
	frame, layout := frameBounds(hwnd), layoutBounds(hwnd)

	// The border is the difference between the two rectangles as they are
	// right now, which is more reliable than assuming a fixed width: it varies
	// with window style, theme, and DPI.
	target := core.Rect{
		X: want.X - (frame.X - layout.X),
		Y: want.Y - (frame.Y - layout.Y),
		W: want.W + (layout.W - frame.W),
		H: want.H + (layout.H - frame.H),
	}

	// A minimised window cannot be positioned; restore it first, exactly as a
	// user would have to.
	if isMinimised(hwnd) {
		procShowWindow.Call(hwnd, swRestore)
	}

	ret, _, err := procSetWindowPos.Call(hwnd, 0,
		uintptr(int32(target.X)), uintptr(int32(target.Y)),
		uintptr(int32(target.W)), uintptr(int32(target.H)),
		swpNoZOrder|swpNoActivate)
	if ret == 0 {
		return err
	}
	return nil
}

// foreground raises a window and gives it the keyboard.
//
// SetForegroundWindow alone is unreliable by design: Windows refuses a
// foreground change requested by a process that does not already own the
// foreground, and it reports that refusal as a plain failure. Attaching to the
// current foreground thread's input queue makes this process eligible, which is
// the difference between `focus` working sometimes and working always.
func foreground(hwnd uintptr) error {
	if isMinimised(hwnd) {
		procShowWindow.Call(hwnd, swRestore)
	}

	current, _, _ := procGetForegroundWnd.Call()
	if current == hwnd {
		return nil
	}

	var targetPID uint32
	targetThread, _, _ := procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&targetPID)))

	var foregroundPID uint32
	foregroundThread, _, _ := procGetWindowThreadPID.Call(current, uintptr(unsafe.Pointer(&foregroundPID)))

	ourThread, _, _ := procGetCurrentThreadId.Call()

	attached := map[uintptr]bool{}
	attach := func(thread uintptr) {
		if thread == 0 || thread == ourThread || attached[thread] {
			return
		}
		if ret, _, _ := procAttachThreadInput.Call(ourThread, thread, 1); ret != 0 {
			attached[thread] = true
		}
	}
	attach(foregroundThread)
	attach(targetThread)
	defer func() {
		for thread := range attached {
			procAttachThreadInput.Call(ourThread, thread, 0)
		}
	}()

	procBringWindowToTop.Call(hwnd)
	ret, _, err := procSetForegroundWnd.Call(hwnd)
	if ret == 0 {
		return err
	}
	return nil
}
