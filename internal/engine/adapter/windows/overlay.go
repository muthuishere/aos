//go:build windows

package windows

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

// The overlay is a per-pixel-alpha layered window: WS_EX_LAYERED +
// UpdateLayeredWindow gives true transparency, WS_EX_TRANSPARENT makes
// every click fall through to the window below, WS_EX_NOACTIVATE +
// SW_SHOWNOACTIVATE keep keyboard focus where it was, and
// WS_EX_TOOLWINDOW keeps it out of the taskbar and Alt-Tab. All of it is
// plain user32/gdi32 in the user's own session: no admin rights, no
// elevation. It cannot appear on the UAC secure desktop (nothing can),
// and exclusive-fullscreen games draw over every window.

var (
	shcore                = windows.NewLazySystemDLL("shcore.dll")
	ovRegisterClassExW    = user32.NewProc("RegisterClassExW")
	ovUnregisterClassW    = user32.NewProc("UnregisterClassW")
	ovCreateWindowExW     = user32.NewProc("CreateWindowExW")
	ovDestroyWindow       = user32.NewProc("DestroyWindow")
	ovDefWindowProcW      = user32.NewProc("DefWindowProcW")
	ovUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	ovShowWindow          = user32.NewProc("ShowWindow")
	ovSetWindowPos        = user32.NewProc("SetWindowPos")
	ovPeekMessageW        = user32.NewProc("PeekMessageW")
	ovTranslateMessage    = user32.NewProc("TranslateMessage")
	ovDispatchMessageW    = user32.NewProc("DispatchMessageW")
	ovGetDC               = user32.NewProc("GetDC")
	ovReleaseDC           = user32.NewProc("ReleaseDC")
	ovMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	ovCreateCompatibleDC  = gdi32.NewProc("CreateCompatibleDC")
	ovDeleteDC            = gdi32.NewProc("DeleteDC")
	ovCreateDIBSection    = gdi32.NewProc("CreateDIBSection")
	ovSelectObject        = gdi32.NewProc("SelectObject")
	ovDeleteObject        = gdi32.NewProc("DeleteObject")
	ovGetDpiForMonitor    = shcore.NewProc("GetDpiForMonitor")
	ovGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
)

const (
	wsExLayered             = 0x00080000
	wsExTransparent         = 0x00000020
	wsExTopmost             = 0x00000008
	wsExNoActivate          = 0x08000000
	wsPopup                 = 0x80000000
	swShowNoActivate        = 4
	swpNoSize               = 0x0001
	swpNoMove               = 0x0002
	ulwAlpha                = 0x00000002
	pmRemove                = 0x0001
	monitorDefaultToNearest = 2
)

var hwndTopmost = ^uintptr(0) // (HWND)-1

type wndClassEx struct {
	CbSize        uint32
	Style         uint32
	LpfnWndProc   uintptr
	CbClsExtra    int32
	CbWndExtra    int32
	HInstance     uintptr
	HIcon         uintptr
	HCursor       uintptr
	HbrBackground uintptr
	LpszMenuName  *uint16
	LpszClassName *uint16
	HIconSm       uintptr
}

type winSize struct{ CX, CY int32 }

type blendFunction struct {
	BlendOp, BlendFlags, SourceConstantAlpha, AlphaFormat byte
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

// OverlayScale: coordinates are physical pixels (the process is
// per-monitor DPI aware), so drawings scale by the monitor's DPI/96 to
// keep the same physical size as on a 100% display, at 1 image pixel
// per coordinate.
func (a *Adapter) OverlayScale(b core.Rect) (float64, float64) {
	if ovGetDpiForMonitor.Find() != nil {
		return 1, 1
	}
	pt := uintptr(uint32(int32(b.X+b.W/2))) | uintptr(uint32(int32(b.Y+b.H/2)))<<32
	mon, _, _ := ovMonitorFromPoint.Call(pt, monitorDefaultToNearest)
	var dx, dy uint32
	if r, _, _ := ovGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r != 0 || dx == 0 {
		return 1, 1
	}
	return float64(dx) / 96, 1
}

func (a *Adapter) ShowOverlay(b core.Rect, img *image.RGBA, done func() bool) error {
	// A window belongs to the thread that created it; its messages must
	// be pumped on that same thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 {
		return errors.New("draw: empty overlay image")
	}
	hinst, _, _ := ovGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString(fmt.Sprintf("windowctl-draw-%d", time.Now().UnixNano()))
	wc := wndClassEx{LpfnWndProc: ovDefWindowProcW.Addr(), HInstance: hinst, LpszClassName: className}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := ovRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("draw: RegisterClassEx: %v", err)
	}
	defer ovUnregisterClassW.Call(uintptr(unsafe.Pointer(className)), hinst)

	hwnd, _, err := ovCreateWindowExW.Call(
		wsExLayered|wsExTransparent|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(className)), 0, wsPopup,
		uintptr(b.X), uintptr(b.Y), uintptr(w), uintptr(h), 0, 0, hinst, 0)
	if hwnd == 0 {
		return fmt.Errorf("draw: CreateWindowEx: %v", err)
	}
	defer ovDestroyWindow.Call(hwnd)

	screen, _, _ := ovGetDC.Call(0)
	defer ovReleaseDC.Call(0, screen)
	mem, _, _ := ovCreateCompatibleDC.Call(screen)
	defer ovDeleteDC.Call(mem)

	// Top-down 32-bpp DIB; UpdateLayeredWindow wants premultiplied BGRA,
	// and image.RGBA is already premultiplied, so only swap R and B.
	bih := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bih.Size = uint32(unsafe.Sizeof(bih))
	var bits unsafe.Pointer
	dib, _, err := ovCreateDIBSection.Call(screen, uintptr(unsafe.Pointer(&bih)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		return fmt.Errorf("draw: CreateDIBSection: %v", err)
	}
	defer ovDeleteObject.Call(dib)
	dst := unsafe.Slice((*byte)(bits), w*h*4)
	for y := 0; y < h; y++ {
		src := img.Pix[y*img.Stride : y*img.Stride+w*4]
		row := dst[y*w*4 : (y+1)*w*4]
		for i := 0; i < len(src); i += 4 {
			row[i], row[i+1], row[i+2], row[i+3] = src[i+2], src[i+1], src[i], src[i+3]
		}
	}
	old, _, _ := ovSelectObject.Call(mem, dib)
	defer ovSelectObject.Call(mem, old)

	pos := point{int32(b.X), int32(b.Y)}
	size := winSize{int32(w), int32(h)}
	var zero point
	blend := blendFunction{BlendOp: 0, SourceConstantAlpha: 255, AlphaFormat: 1} // AC_SRC_OVER, AC_SRC_ALPHA
	if r, _, err := ovUpdateLayeredWindow.Call(hwnd, screen, uintptr(unsafe.Pointer(&pos)), uintptr(unsafe.Pointer(&size)),
		mem, uintptr(unsafe.Pointer(&zero)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha); r == 0 {
		return fmt.Errorf("draw: UpdateLayeredWindow: %v", err)
	}
	ovShowWindow.Call(hwnd, swShowNoActivate)

	var msg winMsg
	lastTop := time.Time{}
	for !done() {
		for {
			r, _, _ := ovPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
			if r == 0 {
				break
			}
			ovTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			ovDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
		// Other topmost windows (a freshly opened topmost app, the
		// taskbar) can rise above us; re-assert the top of the band.
		if time.Since(lastTop) > 500*time.Millisecond {
			ovSetWindowPos.Call(hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
			lastTop = time.Now()
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}
