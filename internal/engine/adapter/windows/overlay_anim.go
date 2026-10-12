//go:build windows

package windows

import (
	"errors"
	"fmt"
	"image"
	"math"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

// Animated overlays (core.OverlayAnimator): the same layered,
// click-through, no-activate window as ShowOverlay, kept open so its
// picture (the DIB) and opacity (SourceConstantAlpha) can change. All
// calls come from one goroutine locked to its thread by the caller.

var ovSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")

const spiGetClientAreaAnimation = 0x1042

var _ core.OverlayAnimator = (*Adapter)(nil)

type overlay struct {
	hwnd, screen, mem, dib, old uintptr
	class                       *uint16
	hinst                       uintptr
	bits                        []byte
	w, h                        int
	pos                         point
	alpha                       byte
}

// live overlays, re-asserted topmost while pumping.
var (
	liveOverlays []*overlay
	lastTopmost  time.Time
)

func (a *Adapter) OpenOverlay(b core.Rect, img *image.RGBA, opacity float64) (core.Overlay, error) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("draw: empty overlay image")
	}
	o := &overlay{w: w, h: h, pos: point{int32(b.X), int32(b.Y)}, alpha: alphaByte(opacity)}
	o.hinst, _, _ = ovGetModuleHandleW.Call(0)
	o.class, _ = windows.UTF16PtrFromString(fmt.Sprintf("windowctl-draw-%d", time.Now().UnixNano()))
	wc := wndClassEx{LpfnWndProc: ovDefWindowProcW.Addr(), HInstance: o.hinst, LpszClassName: o.class}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := ovRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return nil, fmt.Errorf("draw: RegisterClassEx: %v", err)
	}
	var err error
	o.hwnd, _, err = ovCreateWindowExW.Call(
		wsExLayered|wsExTransparent|wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(o.class)), 0, wsPopup,
		uintptr(b.X), uintptr(b.Y), uintptr(w), uintptr(h), 0, 0, o.hinst, 0)
	if o.hwnd == 0 {
		o.Close()
		return nil, fmt.Errorf("draw: CreateWindowEx: %v", err)
	}
	o.screen, _, _ = ovGetDC.Call(0)
	o.mem, _, _ = ovCreateCompatibleDC.Call(o.screen)
	bih := bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bih.Size = uint32(unsafe.Sizeof(bih))
	var bits unsafe.Pointer
	o.dib, _, err = ovCreateDIBSection.Call(o.screen, uintptr(unsafe.Pointer(&bih)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if o.dib == 0 || bits == nil {
		o.Close()
		return nil, fmt.Errorf("draw: CreateDIBSection: %v", err)
	}
	o.bits = unsafe.Slice((*byte)(bits), w*h*4)
	o.old, _, _ = ovSelectObject.Call(o.mem, o.dib)
	o.copyIn(img)
	if err := o.update(); err != nil {
		o.Close()
		return nil, err
	}
	ovShowWindow.Call(o.hwnd, swShowNoActivate)
	liveOverlays = append(liveOverlays, o)
	return o, nil
}

func alphaByte(v float64) byte { return byte(math.Round(math.Max(0, math.Min(1, v)) * 255)) }

// copyIn converts premultiplied RGBA to the DIB's premultiplied BGRA.
func (o *overlay) copyIn(img *image.RGBA) {
	for y := 0; y < o.h; y++ {
		src := img.Pix[y*img.Stride : y*img.Stride+o.w*4]
		row := o.bits[y*o.w*4 : (y+1)*o.w*4]
		for i := 0; i < len(src); i += 4 {
			row[i], row[i+1], row[i+2], row[i+3] = src[i+2], src[i+1], src[i], src[i+3]
		}
	}
}

func (o *overlay) update() error {
	size := winSize{int32(o.w), int32(o.h)}
	var zero point
	blend := blendFunction{BlendOp: 0, SourceConstantAlpha: o.alpha, AlphaFormat: 1} // AC_SRC_OVER, AC_SRC_ALPHA
	if r, _, err := ovUpdateLayeredWindow.Call(o.hwnd, o.screen, uintptr(unsafe.Pointer(&o.pos)), uintptr(unsafe.Pointer(&size)),
		o.mem, uintptr(unsafe.Pointer(&zero)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha); r == 0 {
		return fmt.Errorf("draw: UpdateLayeredWindow: %v", err)
	}
	return nil
}

func (o *overlay) SetImage(img *image.RGBA) error {
	if o.hwnd == 0 {
		return errors.New("draw: overlay closed")
	}
	if img.Rect.Dx() != o.w || img.Rect.Dy() != o.h {
		return errors.New("draw: overlay frame size changed")
	}
	o.copyIn(img)
	return o.update()
}

func (o *overlay) SetOpacity(v float64) {
	if a := alphaByte(v); o.hwnd != 0 && a != o.alpha {
		o.alpha = a
		_ = o.update()
	}
}

func (o *overlay) Close() {
	for i, l := range liveOverlays {
		if l == o {
			liveOverlays = append(liveOverlays[:i], liveOverlays[i+1:]...)
			break
		}
	}
	if o.hwnd != 0 {
		ovDestroyWindow.Call(o.hwnd)
		o.hwnd = 0
	}
	if o.old != 0 {
		ovSelectObject.Call(o.mem, o.old)
		o.old = 0
	}
	if o.dib != 0 {
		ovDeleteObject.Call(o.dib)
		o.dib = 0
	}
	if o.mem != 0 {
		ovDeleteDC.Call(o.mem)
		o.mem = 0
	}
	if o.screen != 0 {
		ovReleaseDC.Call(0, o.screen)
		o.screen = 0
	}
	if o.class != nil {
		ovUnregisterClassW.Call(uintptr(unsafe.Pointer(o.class)), o.hinst)
		o.class = nil
	}
}

// PumpOverlays drains this thread's messages, keeps the overlays at the
// top of the topmost band, and sleeps out the rest of d.
func (a *Adapter) PumpOverlays(d time.Duration) {
	end := time.Now().Add(d)
	var msg winMsg
	for {
		r, _, _ := ovPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmRemove)
		if r == 0 {
			break
		}
		ovTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		ovDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	if time.Since(lastTopmost) > 500*time.Millisecond {
		for _, o := range liveOverlays {
			ovSetWindowPos.Call(o.hwnd, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		}
		lastTopmost = time.Now()
	}
	if left := time.Until(end); left > 0 {
		time.Sleep(left)
	}
}

// ReduceMotion: "Show animations in Windows" off (SPI_GETCLIENTAREAANIMATION).
func (a *Adapter) ReduceMotion() bool {
	var on int32 = 1
	if r, _, _ := ovSystemParametersInfoW.Call(spiGetClientAreaAnimation, 0, uintptr(unsafe.Pointer(&on)), 0); r == 0 {
		return false
	}
	return on == 0
}
