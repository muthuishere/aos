//go:build windows

package windows

// Text finding on Windows, pure Go over COM vtables (no cgo):
//
//   - AccessibleTexts: UI Automation (IUIAutomation, in-box since
//     Windows 7) — every named descendant of the window with its
//     bounding rectangle and control type.
//   - RecognizeText: WinRT Windows.Media.Ocr (in-box since Windows 10,
//     on-device) over a capture of the rect.
//
// Both run in the user's session with no admin rights.

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

var (
	ole32                       = windows.NewLazySystemDLL("ole32.dll")
	oleaut32                    = windows.NewLazySystemDLL("oleaut32.dll")
	combase                     = windows.NewLazySystemDLL("combase.dll")
	procCoInitializeEx          = ole32.NewProc("CoInitializeEx")
	procCoUninitialize          = ole32.NewProc("CoUninitialize")
	procCoCreateInstance        = ole32.NewProc("CoCreateInstance")
	procSysFreeString           = oleaut32.NewProc("SysFreeString")
	procSysStringLen            = oleaut32.NewProc("SysStringLen")
	procRoInitialize            = combase.NewProc("RoInitialize")
	procRoGetActivationFactory  = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString     = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString     = combase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuff = combase.NewProc("WindowsGetStringRawBuffer")
)

var (
	clsidCUIAutomation        = windows.GUID{Data1: 0xFF48DBA4, Data2: 0x60EF, Data3: 0x4201, Data4: [8]byte{0xAA, 0x87, 0x54, 0x10, 0x3E, 0xEF, 0x59, 0x4E}}
	iidIUIAutomation          = windows.GUID{Data1: 0x30CBE57D, Data2: 0xD9D0, Data3: 0x452A, Data4: [8]byte{0xAB, 0x13, 0x7A, 0xC5, 0xAC, 0x48, 0x25, 0xEE}}
	iidIOcrEngineStatics      = windows.GUID{Data1: 0x5BFFA85A, Data2: 0x3384, Data3: 0x3540, Data4: [8]byte{0x99, 0x40, 0x69, 0x91, 0x20, 0xD4, 0x28, 0xA8}}
	iidISoftwareBitmapStatics = windows.GUID{Data1: 0xDF0385DB, Data2: 0x672F, Data3: 0x4A9D, Data4: [8]byte{0x80, 0x6E, 0xC2, 0x44, 0x2F, 0x34, 0x3E, 0x86}}
	iidICryptoBufferStatics   = windows.GUID{Data1: 0x320B7E22, Data2: 0x3CB0, Data3: 0x4CDF, Data4: [8]byte{0x86, 0x63, 0x1D, 0x28, 0x91, 0x00, 0x65, 0xEB}}
	iidIAsyncInfo             = windows.GUID{Data1: 0x00000036, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

// sysPtr reinterprets an address the OS handed back (a COM interface,
// a BSTR, an HSTRING buffer) as a pointer. These live in OS-owned
// memory the Go GC never moves, which is the case go vet cannot see.
func sysPtr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

// vcall invokes method idx of a COM interface pointer and returns the HRESULT.
func vcall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(sysPtr(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func release(obj uintptr) {
	if obj != 0 {
		vcall(obj, 2)
	}
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

func hrErr(what string, hr uintptr) error {
	return fmt.Errorf("windowctl windows: %s failed (HRESULT 0x%08X)", what, uint32(hr))
}

func bstrToString(b uintptr) string {
	if b == 0 {
		return ""
	}
	n, _, _ := procSysStringLen.Call(b)
	return windows.UTF16ToString(unsafe.Slice((*uint16)(sysPtr(b)), n))
}

// ---- UI Automation ---------------------------------------------------

// IUIAutomation / IUIAutomationElement / IUIAutomationElementArray
// vtable slots (IUnknown occupies 0..2).
const (
	uiaElementFromHandle   = 6
	uiaCreateTrueCondition = 21
	elFindAll              = 6
	elCurrentControlType   = 21
	elCurrentName          = 23
	elCurrentIsOffscreen   = 38
	elCurrentBoundingRect  = 43
	arrLength              = 3
	arrGetElement          = 4
	treeScopeDescendants   = 4
)

func uiaRole(ct int32) string {
	switch ct {
	case 50000, 50031: // Button, SplitButton
		return "button"
	case 50009, 50010, 50011: // Menu, MenuBar, MenuItem
		return "menuitem"
	case 50005:
		return "link"
	case 50019:
		return "tab"
	case 50002:
		return "checkbox"
	case 50013:
		return "radio"
	case 50003, 50004: // ComboBox, Edit
		return "textfield"
	case 50020:
		return "text"
	case 50006:
		return "image"
	}
	return "other"
}

func (a *Adapter) AccessibleTexts(id string) ([]core.TextItem, error) {
	hwnd, err := parseHwnd(id)
	if err != nil {
		return nil, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, 2 /* COINIT_APARTMENTTHREADED */); hr == 0 || hr == 1 {
		defer procCoUninitialize.Call()
	}
	var uia uintptr
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidCUIAutomation)), 0, 1|4, /* INPROC|LOCAL */
		uintptr(unsafe.Pointer(&iidIUIAutomation)), uintptr(unsafe.Pointer(&uia))); failed(hr) || uia == 0 {
		return nil, hrErr("CoCreateInstance(CUIAutomation)", hr)
	}
	defer release(uia)
	var root uintptr
	if hr := vcall(uia, uiaElementFromHandle, hwnd, uintptr(unsafe.Pointer(&root))); failed(hr) || root == 0 {
		return nil, fmt.Errorf("%w: window %s is not in the UI Automation tree", core.ErrNoMatch, id)
	}
	defer release(root)
	var cond uintptr
	if hr := vcall(uia, uiaCreateTrueCondition, uintptr(unsafe.Pointer(&cond))); failed(hr) {
		return nil, hrErr("CreateTrueCondition", hr)
	}
	defer release(cond)
	var arr uintptr
	if hr := vcall(root, elFindAll, treeScopeDescendants, cond, uintptr(unsafe.Pointer(&arr))); failed(hr) || arr == 0 {
		return nil, hrErr("FindAll", hr)
	}
	defer release(arr)
	var n int32
	vcall(arr, arrLength, uintptr(unsafe.Pointer(&n)))
	if n > 8000 {
		n = 8000
	}
	out := make([]core.TextItem, 0, n)
	for i := int32(0); i < n; i++ {
		var el uintptr
		if hr := vcall(arr, arrGetElement, uintptr(i), uintptr(unsafe.Pointer(&el))); failed(hr) || el == 0 {
			continue
		}
		var off int32
		vcall(el, elCurrentIsOffscreen, uintptr(unsafe.Pointer(&off)))
		var name uintptr
		vcall(el, elCurrentName, uintptr(unsafe.Pointer(&name)))
		text := strings.TrimSpace(bstrToString(name))
		if name != 0 {
			procSysFreeString.Call(name)
		}
		if off == 0 && text != "" {
			var r rect
			var ct int32
			vcall(el, elCurrentBoundingRect, uintptr(unsafe.Pointer(&r)))
			vcall(el, elCurrentControlType, uintptr(unsafe.Pointer(&ct)))
			if r.Right > r.Left && r.Bottom > r.Top {
				out = append(out, core.TextItem{
					Text: text, Role: uiaRole(ct), Source: "accessibility",
					Bounds: core.Rect{X: int(r.Left), Y: int(r.Top), W: int(r.Right - r.Left), H: int(r.Bottom - r.Top)},
				})
			}
		}
		release(el)
	}
	return out, nil
}

// ---- Windows.Media.Ocr ----------------------------------------------

// WinRT vtable slots (IInspectable occupies 0..5).
const (
	ocrStaticsMaxImageDimension = 6
	ocrStaticsTryCreateFromUser = 10
	ocrEngineRecognizeAsync     = 6
	asyncInfoStatus             = 7
	asyncOpGetResults           = 8
	ocrResultLines              = 6
	ocrLineWords                = 6
	ocrLineText                 = 7
	ocrWordBoundingRect         = 6
	ocrWordText                 = 7
	vectorGetAt                 = 6
	vectorSize                  = 7
	sbStaticsCreateCopyFromBuf  = 9
	cbStaticsCreateFromByteArr  = 9
	bitmapPixelFormatBgra8      = 87
)

func hstring(s string) (uintptr, error) {
	u, _ := windows.UTF16FromString(s)
	var h uintptr
	if hr, _, _ := procWindowsCreateString.Call(uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h))); failed(hr) {
		return 0, hrErr("WindowsCreateString", hr)
	}
	return h, nil
}

func hstringToString(h uintptr) string {
	if h == 0 {
		return ""
	}
	var n uint32
	p, _, _ := procWindowsGetStringRawBuff.Call(h, uintptr(unsafe.Pointer(&n)))
	if p == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(sysPtr(p)), n))
}

func factory(class string, iid *windows.GUID) (uintptr, error) {
	h, err := hstring(class)
	if err != nil {
		return 0, err
	}
	defer procWindowsDeleteString.Call(h)
	var f uintptr
	if hr, _, _ := procRoGetActivationFactory.Call(h, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&f))); failed(hr) || f == 0 {
		return 0, hrErr("RoGetActivationFactory("+class+")", hr)
	}
	return f, nil
}

type winrtRect struct{ X, Y, W, H float32 }

func (a *Adapter) RecognizeText(b core.Rect) ([]core.TextItem, error) {
	if b.W <= 0 || b.H <= 0 {
		return nil, errors.New("windowctl windows: empty OCR region")
	}
	// Capture through the existing (DPI-correct, 1 px == 1 coordinate) path.
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("windowctl-ocr-%d.png", time.Now().UnixNano()))
	defer os.Remove(tmp)
	if err := a.CaptureRect(b, tmp); err != nil {
		return nil, err
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, err
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		return nil, err
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procRoInitialize.Call(1) // RO_INIT_MULTITHREADED; S_FALSE / changed-mode are fine

	ocrStatics, err := factory("Windows.Media.Ocr.OcrEngine", &iidIOcrEngineStatics)
	if err != nil {
		return nil, err
	}
	defer release(ocrStatics)
	var maxDim uint32
	vcall(ocrStatics, ocrStaticsMaxImageDimension, uintptr(unsafe.Pointer(&maxDim)))

	// Downscale only if the engine's limit demands it; boxes scale back.
	scale := 1.0
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if maxDim > 0 && (uint32(w) > maxDim || uint32(h) > maxDim) {
		scale = float64(maxDim) / float64(max(w, h))
		w, h = int(float64(w)*scale), int(float64(h)*scale)
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(rgba, rgba.Rect, src, src.Bounds(), xdraw.Src, nil)
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i], rgba.Pix[i+2] = rgba.Pix[i+2], rgba.Pix[i] // RGBA → BGRA
	}

	cb, err := factory("Windows.Security.Cryptography.CryptographicBuffer", &iidICryptoBufferStatics)
	if err != nil {
		return nil, err
	}
	defer release(cb)
	var buf uintptr
	if hr := vcall(cb, cbStaticsCreateFromByteArr, uintptr(len(rgba.Pix)), uintptr(unsafe.Pointer(&rgba.Pix[0])), uintptr(unsafe.Pointer(&buf))); failed(hr) {
		return nil, hrErr("CryptographicBuffer.CreateFromByteArray", hr)
	}
	defer release(buf)
	sb, err := factory("Windows.Graphics.Imaging.SoftwareBitmap", &iidISoftwareBitmapStatics)
	if err != nil {
		return nil, err
	}
	defer release(sb)
	var bmp uintptr
	if hr := vcall(sb, sbStaticsCreateCopyFromBuf, buf, bitmapPixelFormatBgra8, uintptr(w), uintptr(h), uintptr(unsafe.Pointer(&bmp))); failed(hr) {
		return nil, hrErr("SoftwareBitmap.CreateCopyFromBuffer", hr)
	}
	defer release(bmp)

	var engine uintptr
	if hr := vcall(ocrStatics, ocrStaticsTryCreateFromUser, uintptr(unsafe.Pointer(&engine))); failed(hr) || engine == 0 {
		return nil, errors.New("windowctl windows: no OCR language installed for this user (Settings → Time & language → Language: add a language with the 'Optical character recognition' feature)")
	}
	defer release(engine)
	var op uintptr
	if hr := vcall(engine, ocrEngineRecognizeAsync, bmp, uintptr(unsafe.Pointer(&op))); failed(hr) {
		return nil, hrErr("OcrEngine.RecognizeAsync", hr)
	}
	defer release(op)
	var info uintptr
	if hr := vcall(op, 0, uintptr(unsafe.Pointer(&iidIAsyncInfo)), uintptr(unsafe.Pointer(&info))); failed(hr) {
		return nil, hrErr("QueryInterface(IAsyncInfo)", hr)
	}
	defer release(info)
	deadline := time.Now().Add(20 * time.Second)
	for {
		var status int32
		vcall(info, asyncInfoStatus, uintptr(unsafe.Pointer(&status)))
		if status == 1 {
			break
		}
		if status > 1 {
			return nil, fmt.Errorf("windowctl windows: OCR failed (async status %d)", status)
		}
		if time.Now().After(deadline) {
			return nil, errors.New("windowctl windows: OCR timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var result uintptr
	if hr := vcall(op, asyncOpGetResults, uintptr(unsafe.Pointer(&result))); failed(hr) || result == 0 {
		return nil, hrErr("RecognizeAsync.GetResults", hr)
	}
	defer release(result)

	toGlobal := func(r winrtRect) core.Rect {
		return core.Rect{
			X: b.X + int(float64(r.X)/scale), Y: b.Y + int(float64(r.Y)/scale),
			W: int(float64(r.W)/scale + 0.5), H: int(float64(r.H)/scale + 0.5),
		}
	}
	var lines uintptr
	if hr := vcall(result, ocrResultLines, uintptr(unsafe.Pointer(&lines))); failed(hr) {
		return nil, hrErr("OcrResult.Lines", hr)
	}
	defer release(lines)
	var nLines uint32
	vcall(lines, vectorSize, uintptr(unsafe.Pointer(&nLines)))
	var out []core.TextItem
	for i := uint32(0); i < nLines; i++ {
		var line uintptr
		if failed(vcall(lines, vectorGetAt, uintptr(i), uintptr(unsafe.Pointer(&line)))) || line == 0 {
			continue
		}
		var lt uintptr
		vcall(line, ocrLineText, uintptr(unsafe.Pointer(&lt)))
		item := core.TextItem{Text: hstringToString(lt), Source: "ocr", Role: "text"}
		procWindowsDeleteString.Call(lt)
		var words uintptr
		vcall(line, ocrLineWords, uintptr(unsafe.Pointer(&words)))
		var nWords uint32
		if words != 0 {
			vcall(words, vectorSize, uintptr(unsafe.Pointer(&nWords)))
		}
		var x0, y0, x1, y1 int
		for j := uint32(0); j < nWords; j++ {
			var word uintptr
			if failed(vcall(words, vectorGetAt, uintptr(j), uintptr(unsafe.Pointer(&word)))) || word == 0 {
				continue
			}
			var r winrtRect
			vcall(word, ocrWordBoundingRect, uintptr(unsafe.Pointer(&r)))
			var wt uintptr
			vcall(word, ocrWordText, uintptr(unsafe.Pointer(&wt)))
			g := toGlobal(r)
			item.Words = append(item.Words, core.TextItem{Text: hstringToString(wt), Bounds: g, Source: "ocr", Role: "text"})
			procWindowsDeleteString.Call(wt)
			release(word)
			if len(item.Words) == 1 {
				x0, y0, x1, y1 = g.X, g.Y, g.X+g.W, g.Y+g.H
			} else {
				x0, y0, x1, y1 = min(x0, g.X), min(y0, g.Y), max(x1, g.X+g.W), max(y1, g.Y+g.H)
			}
		}
		release(words)
		release(line)
		// An OcrLine has no box of its own: it is the union of its words.
		item.Bounds = core.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
		if item.Text != "" && item.Bounds.W > 0 {
			out = append(out, item)
		}
	}
	return out, nil
}
