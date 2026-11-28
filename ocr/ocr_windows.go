//go:build windows

package ocr

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

// Windows: WinRT Windows.Media.Ocr (in-box since Windows 10, on device)
// over COM vtables with syscall, so no cgo and no PowerShell. Same engine
// and steps as aos's own screen OCR.

var (
	combase                     = windows.NewLazySystemDLL("combase.dll")
	procRoInitialize            = combase.NewProc("RoInitialize")
	procRoGetActivationFactory  = combase.NewProc("RoGetActivationFactory")
	procWindowsCreateString     = combase.NewProc("WindowsCreateString")
	procWindowsDeleteString     = combase.NewProc("WindowsDeleteString")
	procWindowsGetStringRawBuff = combase.NewProc("WindowsGetStringRawBuffer")

	iidIOcrEngineStatics      = windows.GUID{Data1: 0x5BFFA85A, Data2: 0x3384, Data3: 0x3540, Data4: [8]byte{0x99, 0x40, 0x69, 0x91, 0x20, 0xD4, 0x28, 0xA8}}
	iidISoftwareBitmapStatics = windows.GUID{Data1: 0xDF0385DB, Data2: 0x672F, Data3: 0x4A9D, Data4: [8]byte{0x80, 0x6E, 0xC2, 0x44, 0x2F, 0x34, 0x3E, 0x86}}
	iidICryptoBufferStatics   = windows.GUID{Data1: 0x320B7E22, Data2: 0x3CB0, Data3: 0x4CDF, Data4: [8]byte{0x86, 0x63, 0x1D, 0x28, 0x91, 0x00, 0x65, 0xEB}}
	iidIAsyncInfo             = windows.GUID{Data1: 0x00000036, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

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

func sysPtr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

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
	return fmt.Errorf("ocr: %s failed (HRESULT 0x%08X)", what, uint32(hr))
}

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

func recognize(src image.Image, _ Options) ([]Line, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	procRoInitialize.Call(1) // RO_INIT_MULTITHREADED; S_FALSE / changed mode are fine

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
		w, h = max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
	}
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.ApproxBiLinear.Scale(rgba, rgba.Rect, src, src.Bounds(), xdraw.Src, nil)
	for i := 0; i < len(rgba.Pix); i += 4 {
		rgba.Pix[i], rgba.Pix[i+2] = rgba.Pix[i+2], rgba.Pix[i] // RGBA -> BGRA
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
		return nil, errors.New("ocr: no OCR language installed for this user (Settings > Time & language > Language: add a language with the 'Optical character recognition' feature)")
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
			return nil, fmt.Errorf("ocr: recognition failed (async status %d)", status)
		}
		if time.Now().After(deadline) {
			return nil, errors.New("ocr: recognition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var result uintptr
	if hr := vcall(op, asyncOpGetResults, uintptr(unsafe.Pointer(&result))); failed(hr) || result == 0 {
		return nil, hrErr("RecognizeAsync.GetResults", hr)
	}
	defer release(result)

	var lines uintptr
	if hr := vcall(result, ocrResultLines, uintptr(unsafe.Pointer(&lines))); failed(hr) {
		return nil, hrErr("OcrResult.Lines", hr)
	}
	defer release(lines)
	var nLines uint32
	vcall(lines, vectorSize, uintptr(unsafe.Pointer(&nLines)))
	var out []Line
	for i := uint32(0); i < nLines; i++ {
		var line uintptr
		if failed(vcall(lines, vectorGetAt, uintptr(i), uintptr(unsafe.Pointer(&line)))) || line == 0 {
			continue
		}
		var lt uintptr
		vcall(line, ocrLineText, uintptr(unsafe.Pointer(&lt)))
		item := Line{Text: hstringToString(lt)}
		procWindowsDeleteString.Call(lt)
		var words uintptr
		vcall(line, ocrLineWords, uintptr(unsafe.Pointer(&words)))
		var nWords uint32
		if words != 0 {
			vcall(words, vectorSize, uintptr(unsafe.Pointer(&nWords)))
		}
		var x0, y0, x1, y1 float64
		for j := uint32(0); j < nWords; j++ {
			var word uintptr
			if failed(vcall(words, vectorGetAt, uintptr(j), uintptr(unsafe.Pointer(&word)))) || word == 0 {
				continue
			}
			var r winrtRect
			vcall(word, ocrWordBoundingRect, uintptr(unsafe.Pointer(&r)))
			var wt uintptr
			vcall(word, ocrWordText, uintptr(unsafe.Pointer(&wt)))
			wd := Word{Text: hstringToString(wt),
				X: float64(r.X) / scale, Y: float64(r.Y) / scale, W: float64(r.W) / scale, H: float64(r.H) / scale}
			item.Words = append(item.Words, wd)
			procWindowsDeleteString.Call(wt)
			release(word)
			if len(item.Words) == 1 {
				x0, y0, x1, y1 = wd.X, wd.Y, wd.X+wd.W, wd.Y+wd.H
			} else {
				x0, y0, x1, y1 = min(x0, wd.X), min(y0, wd.Y), max(x1, wd.X+wd.W), max(y1, wd.Y+wd.H)
			}
		}
		release(words)
		release(line)
		// An OcrLine has no box of its own: it is the union of its words.
		item.X, item.Y, item.W, item.H = x0, y0, x1-x0, y1-y0
		if item.Text != "" && item.W > 0 {
			out = append(out, item)
		}
	}
	return out, nil
}
