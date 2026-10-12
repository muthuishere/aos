//go:build windows

// Package windows is the Windows platform adapter for windowctl.
//
// Implemented against user32.dll via golang.org/x/sys/windows. ListWindows
// enumerates top-level windows via EnumWindows; ListMonitors enumerates
// every attached display via EnumDisplayMonitors + GetMonitorInfoW.
package windows

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows         = user32.NewProc("EnumWindows")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procGetWindowTextLenW   = user32.NewProc("GetWindowTextLengthW")
	procIsWindowVisible     = user32.NewProc("IsWindowVisible")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
	procSetForegroundWnd    = user32.NewProc("SetForegroundWindow")
	procGetWindowThreadPID  = user32.NewProc("GetWindowThreadProcessId")
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procGetForegroundWnd    = user32.NewProc("GetForegroundWindow")
)

type point struct{ X, Y int32 }

type rect struct{ Left, Top, Right, Bottom int32 }

// monitorInfo mirrors the Win32 MONITORINFO struct exactly:
//
//	typedef struct tagMONITORINFO {
//	    DWORD cbSize;     // must be set to sizeof(MONITORINFO) = 40
//	    RECT  rcMonitor;  // full display bounds (virtual-screen coords)
//	    RECT  rcWork;     // work-area (excludes taskbar etc.)
//	    DWORD dwFlags;    // bit 0 = MONITORINFOF_PRIMARY
//	} MONITORINFO;
//
// Total size: 4 + 16 + 16 + 4 = 40 bytes. We use plain MONITORINFO
// (not MONITORINFOEXW) because the spec only needs ID/X/Y/W/H/Primary —
// the device-name field would be dead weight.
type monitorInfo struct {
	CbSize    uint32
	RcMonitor rect
	RcWork    rect
	DwFlags   uint32
}

const monitorInfofPrimary uint32 = 1

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) ListWindows() ([]core.Window, error) {
	var out []core.Window
	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		visible, _, _ := procIsWindowVisible.Call(hwnd)
		if visible == 0 {
			return 1
		}
		titleLen, _, _ := procGetWindowTextLenW.Call(hwnd)
		if titleLen == 0 {
			return 1
		}
		buf := make([]uint16, titleLen+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), titleLen+1)
		title := windows.UTF16ToString(buf)

		// IsWindowVisible is necessary but not sufficient: it is true for
		// compositor-cloaked UWP apps, tool palettes, and the desktop itself.
		if !listable(hwnd, title) {
			return 1
		}

		var pid uint32
		procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))

		out = append(out, core.Window{
			ID:     fmt.Sprintf("%d", hwnd),
			Title:  title,
			App:    appName(pid),
			PID:    int(pid),
			Bounds: frameBounds(hwnd),
		})
		return 1
	})
	ret, _, err := procEnumWindows.Call(cb, 0)
	if ret == 0 {
		return nil, fmt.Errorf("EnumWindows: %w", err)
	}
	return out, nil
}

// ListMonitors enumerates every attached display via Win32
// EnumDisplayMonitors, then resolves each HMONITOR's bounds and
// primary-flag with GetMonitorInfoW. IDs are sequential 0..N-1 in
// enumeration order — same shape the darwin adapter returns so the
// public package can treat all platforms uniformly.
func (a *Adapter) ListMonitors() ([]core.Monitor, error) {
	var out []core.Monitor
	var enumErr error
	cb := syscall.NewCallback(func(hmon, _ uintptr, _ *rect, _ uintptr) uintptr {
		mi := monitorInfo{CbSize: uint32(unsafe.Sizeof(monitorInfo{}))}
		ret, _, err := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
		if ret == 0 {
			enumErr = fmt.Errorf("GetMonitorInfoW: %w", err)
			return 0 // stop enumeration on hard failure
		}
		out = append(out, core.Monitor{
			ID:      len(out),
			X:       int(mi.RcMonitor.Left),
			Y:       int(mi.RcMonitor.Top),
			Width:   int(mi.RcMonitor.Right - mi.RcMonitor.Left),
			Height:  int(mi.RcMonitor.Bottom - mi.RcMonitor.Top),
			Primary: mi.DwFlags&monitorInfofPrimary != 0,
		})
		return 1
	})
	ret, _, err := procEnumDisplayMonitors.Call(0, 0, cb, 0)
	if ret == 0 {
		if enumErr != nil {
			return nil, enumErr
		}
		return nil, fmt.Errorf("EnumDisplayMonitors: %w", err)
	}
	if enumErr != nil {
		return nil, enumErr
	}

	// Stamp Active (cursor) + Focused (foreground window centroid).
	// GetCursorPos / GetForegroundWindow / GetWindowRect are
	// stateless and require no special permissions on Windows, so
	// it's fine to do these on every list call.
	var p point
	if ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); ret != 0 {
		stampMonitorByPoint(out, int(p.X), int(p.Y), func(m *core.Monitor) { m.Active = true })
	}
	hwnd, _, _ := procGetForegroundWnd.Call()
	if hwnd != 0 {
		var r rect
		if ret, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ret != 0 {
			cx := int(r.Left) + (int(r.Right)-int(r.Left))/2
			cy := int(r.Top) + (int(r.Bottom)-int(r.Top))/2
			stampMonitorByPoint(out, cx, cy, func(m *core.Monitor) { m.Focused = true })
		}
	}
	return out, nil
}

// stampMonitorByPoint runs `set` against the first monitor whose
// bounds contain (x, y). Shared between cursor (Active) and
// foreground-centroid (Focused) probes.
func stampMonitorByPoint(ms []core.Monitor, x, y int, set func(*core.Monitor)) {
	for i := range ms {
		m := ms[i]
		if x >= m.X && x < m.X+m.Width && y >= m.Y && y < m.Y+m.Height {
			set(&ms[i])
			return
		}
	}
}

// moveClampToleranceWindows is the per-axis pixel slack we tolerate
// between requested and actual post-move bounds before reporting an
// OS-imposed clamp. DWM frame insets and shadow accounting cause small
// drift on Win10/11 even when the move was honored verbatim; 10px
// absorbs that without hiding real minimum-size clamps (apps like
// Chrome refuse < ~500px).
const moveClampToleranceWindows = 10

func (a *Adapter) Move(id string, b core.Rect) error {
	hwnd, err := parseHwnd(id)
	if err != nil {
		return err
	}
	// positionFrame, not MoveWindow: the caller means the visible rectangle,
	// and Windows positions the larger layout rectangle.
	if err := positionFrame(hwnd, b); err != nil {
		return fmt.Errorf("position %s: %w", id, err)
	}
	// Post-move sanity check: re-read the actual window rect and
	// compare against the requested bounds. MoveWindow reports success
	// even when the app refuses our size via WM_GETMINMAXINFO or DWM
	// animates the window into a clamped frame. Poll until bounds
	// settle (size lands first; X/Y can lag while DWM animates) — same
	// problem darwin had with BUG-13.
	actual, ok := settledHwndBounds(hwnd, b)
	if !ok {
		return nil
	}
	if clampDelta(actual, b) > moveClampToleranceWindows {
		return fmt.Errorf("requested %dx%d at (%d,%d), OS clamped to %dx%d at (%d,%d) (likely a minimum-window-size constraint)",
			b.W, b.H, b.X, b.Y,
			actual.W, actual.H, actual.X, actual.Y)
	}
	return nil
}

// settledHwndBounds reads the hwnd's window rect, returning early if
// the first read already matches `requested` within tolerance, and
// otherwise polling every 40ms until two consecutive reads agree or
// 300ms total elapses. Same shape as darwin's settledWindowBounds —
// see the comment there for the rationale.
func settledHwndBounds(hwnd uintptr, requested core.Rect) (core.Rect, bool) {
	// Read the same rectangle Move positions — the visible frame. Comparing the
	// layout rectangle against a requested frame rectangle would differ by the
	// invisible border on every axis, which is wider than the tolerance, so
	// every successful move would be reported as an OS clamp.
	read := func() (core.Rect, bool) {
		bounds := frameBounds(hwnd)
		if bounds.W <= 0 && bounds.H <= 0 {
			return core.Rect{}, false
		}
		return bounds, true
	}
	actual, ok := read()
	if !ok {
		return core.Rect{}, false
	}
	if clampDelta(actual, requested) <= moveClampToleranceWindows {
		return actual, true
	}
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(40 * time.Millisecond)
		next, ok := read()
		if !ok {
			return actual, true
		}
		if next == actual {
			return actual, true
		}
		actual = next
	}
	return actual, true
}

// clampDelta returns the largest per-axis absolute difference between
// the actual and requested rects. Used to decide whether a post-move
// re-read indicates the OS clamped our request.
func clampDelta(actual, requested core.Rect) int {
	d := abs(actual.X - requested.X)
	if v := abs(actual.Y - requested.Y); v > d {
		d = v
	}
	if v := abs(actual.W - requested.W); v > d {
		d = v
	}
	if v := abs(actual.H - requested.H); v > d {
		d = v
	}
	return d
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (a *Adapter) Focus(id string) error {
	hwnd, err := parseHwnd(id)
	if err != nil {
		return err
	}
	if err := foreground(hwnd); err != nil {
		return fmt.Errorf("focus %s: %w", id, err)
	}
	return nil
}

// RequestAccessibility is a no-op on Windows. The Win32 windowing APIs
// used by the adapter (SetForegroundWindow, MoveWindow) gate on session
// / privilege rather than a per-process trust grant comparable to
// macOS Accessibility. The CLI keeps the method on the interface so
// `windowctl permissions` exists on all three platforms; here it just
// returns nil and the CLI prints a "not required" message based on
// runtime.GOOS.
func (a *Adapter) RequestAccessibility() error { return nil }

// CheckAccessibility is the non-prompting sibling of RequestAccessibility.
// AX is a macOS-only concept; on Windows there is nothing to check, so
// we always report trusted=true.
func (a *Adapter) CheckAccessibility() bool { return true }

func parseHwnd(id string) (uintptr, error) {
	var n uint64
	if _, err := fmt.Sscanf(id, "%d", &n); err != nil {
		return 0, fmt.Errorf("invalid window id %q: %w", id, err)
	}
	return uintptr(n), nil
}
