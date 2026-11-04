package groups

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/sys"
)

var (
	capUser32            = windows.NewLazySystemDLL("user32.dll")
	capKernel32          = windows.NewLazySystemDLL("kernel32.dll")
	procGetSystemMetrics = capUser32.NewProc("GetSystemMetrics")
	procOpenClipboard    = capUser32.NewProc("OpenClipboard")
	procCloseClipboard   = capUser32.NewProc("CloseClipboard")
	procEmptyClipboard   = capUser32.NewProc("EmptyClipboard")
	procSetClipboardData = capUser32.NewProc("SetClipboardData")
	procGetClipboardData = capUser32.NewProc("GetClipboardData")
	procGlobalAlloc      = capKernel32.NewProc("GlobalAlloc")
	procGlobalFree       = capKernel32.NewProc("GlobalFree")
	procGlobalLock       = capKernel32.NewProc("GlobalLock")
	procGlobalUnlock     = capKernel32.NewProc("GlobalUnlock")
	procGlobalSize       = capKernel32.NewProc("GlobalSize")
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	cfDIB             = 8
	cfUnicodeText     = 13
	gmemMoveable      = 0x0002
)

func takeScreenshot(req captureRequest) error {
	if req.Mode != captureFullscreen {
		// Snip & Sketch owns interactive capture on Windows and always hands
		// the result to the clipboard, so honour --copy and reject a --out that
		// it cannot satisfy.
		if !req.Copy {
			return fmt.Errorf("%s capture on windows goes to the clipboard; add --copy", req.Mode)
		}
		_, err := sys.Output("explorer.exe", "ms-screenclip:")
		return err
	}

	// The whole virtual desktop. The engine's Windows adapter makes this
	// process per-monitor DPI aware in its init, so these are real pixels,
	// not a scaled corner of a 250% screen.
	metric := func(i uintptr) int {
		v, _, _ := procGetSystemMetrics.Call(i)
		return int(int32(v))
	}
	virtual := engine.Rect{X: metric(smXVirtualScreen), Y: metric(smYVirtualScreen),
		W: metric(smCXVirtualScreen), H: metric(smCYVirtualScreen)}
	if virtual.W <= 0 || virtual.H <= 0 {
		return fmt.Errorf("could not read the virtual screen size")
	}

	out := req.Path
	if req.Copy {
		out = filepath.Join(os.TempDir(), fmt.Sprintf("aos-shot-%d.png", time.Now().UnixNano()))
		defer os.Remove(out)
	}
	if _, err := engine.Screenshot(engine.ScreenshotOptions{Region: &virtual, OutPath: out}); err != nil {
		return err
	}
	if !req.Copy {
		return nil
	}
	f, err := os.Open(out)
	if err != nil {
		return err
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		return err
	}
	return setClipboardDIB(img)
}

// dibBytes is a packed CF_DIB: a 40-byte BITMAPINFOHEADER followed by 32bpp
// BGRA rows, bottom-up (positive height), as every clipboard reader expects.
func dibBytes(img image.Image) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	buf := make([]byte, 40+w*h*4)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], 40)             // biSize
	le.PutUint32(buf[4:], uint32(w))      // biWidth
	le.PutUint32(buf[8:], uint32(h))      // biHeight (>0: bottom-up)
	le.PutUint16(buf[12:], 1)             // biPlanes
	le.PutUint16(buf[14:], 32)            // biBitCount
	le.PutUint32(buf[20:], uint32(w*h*4)) // biSizeImage; biCompression=BI_RGB(0)
	px := buf[40:]
	for y := 0; y < h; y++ {
		row := px[(h-1-y)*w*4:]
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = byte(bl>>8), byte(g>>8), byte(r>>8), 0xFF
		}
	}
	return buf
}

func setClipboardDIB(img image.Image) error {
	data := dibBytes(img)
	if err := openClipboard(); err != nil {
		return err
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(len(data)))
	if h == 0 {
		return fmt.Errorf("GlobalAlloc failed")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock failed")
	}
	copy(unsafe.Slice((*byte)(osPtr(p)), len(data)), data)
	procGlobalUnlock.Call(h)
	// On success the clipboard owns h; free it only on failure.
	if r, _, err := procSetClipboardData.Call(cfDIB, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData: %v", err)
	}
	return nil
}

// openClipboard retries: another app holding the clipboard is common and brief.
func openClipboard() error {
	var err error
	for i := 0; i < 10; i++ {
		var r uintptr
		if r, _, err = procOpenClipboard.Call(0); r != 0 {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("OpenClipboard: %v", err)
}
