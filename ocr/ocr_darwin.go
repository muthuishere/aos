//go:build darwin

package ocr

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"sync"
	"unicode/utf16"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// macOS: Apple Vision (VNRecognizeTextRequest) through the Objective-C
// runtime with purego, so no cgo. The image goes to Vision as PNG data.

type cgRect struct{ X, Y, W, H float64 }
type nsRange struct{ Location, Length uint64 }

var (
	loadOnce sync.Once
	loadErr  error
	poolPush func() uintptr
	poolPop  func(uintptr)
)

func load() error {
	loadOnce.Do(func() {
		for _, lib := range []string{
			"/System/Library/Frameworks/Foundation.framework/Foundation",
			"/System/Library/Frameworks/Vision.framework/Vision",
		} {
			if _, err := purego.Dlopen(lib, purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
				loadErr = fmt.Errorf("ocr: cannot load %s: %w", lib, err)
				return
			}
		}
		lib, err := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			loadErr = fmt.Errorf("ocr: cannot load libobjc: %w", err)
			return
		}
		purego.RegisterLibFunc(&poolPush, lib, "objc_autoreleasePoolPush")
		purego.RegisterLibFunc(&poolPop, lib, "objc_autoreleasePoolPop")
		if objc.GetClass("VNRecognizeTextRequest") == 0 {
			loadErr = errors.New("ocr: Vision text recognition needs macOS 10.15 or later")
		}
	})
	return loadErr
}

var sel = struct {
	alloc, init, release, dictionary, dataWithBytes, initWithData, setLevel, setCorrection,
	setLanguages, arrayWithObject, array, addObject, stringWithUTF8, perform, results, count,
	objectAtIndex, topCandidates, str, utf8, confidence, boundingBox, boxForRange sel_
}{}

type sel_ = objc.SEL

var selOnce sync.Once

func sels() {
	selOnce.Do(func() {
		r := objc.RegisterName
		sel.alloc, sel.init, sel.release = r("alloc"), r("init"), r("release")
		sel.dictionary, sel.dataWithBytes, sel.initWithData = r("dictionary"), r("dataWithBytes:length:"), r("initWithData:options:")
		sel.setLevel, sel.setCorrection, sel.setLanguages = r("setRecognitionLevel:"), r("setUsesLanguageCorrection:"), r("setRecognitionLanguages:")
		sel.arrayWithObject, sel.array, sel.addObject, sel.stringWithUTF8 = r("arrayWithObject:"), r("array"), r("addObject:"), r("stringWithUTF8String:")
		sel.perform, sel.results, sel.count, sel.objectAtIndex = r("performRequests:error:"), r("results"), r("count"), r("objectAtIndex:")
		sel.topCandidates, sel.str, sel.utf8, sel.confidence = r("topCandidates:"), r("string"), r("UTF8String"), r("confidence")
		sel.boundingBox, sel.boxForRange = r("boundingBox"), r("boundingBoxForRange:error:")
	})
}

func cls(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var b []byte
	for i := uintptr(0); ; i++ {
		c := *(*byte)(unsafe.Add(unsafe.Pointer(nil), p+i)) //nolint:govet // OS-owned C string
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}

func recognize(img image.Image, opts Options) ([]Line, error) {
	if err := load(); err != nil {
		return nil, err
	}
	sels()
	var buf bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("ocr: encode image: %w", err)
	}
	data := buf.Bytes()
	pw, ph := float64(img.Bounds().Dx()), float64(img.Bounds().Dy())

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pool := poolPush()
	defer poolPop(pool)

	nsData := objc.Send[objc.ID](cls("NSData"), sel.dataWithBytes, unsafe.Pointer(&data[0]), uint64(len(data)))
	handler := objc.Send[objc.ID](cls("VNImageRequestHandler"), sel.alloc)
	handler = objc.Send[objc.ID](handler, sel.initWithData, nsData, objc.Send[objc.ID](cls("NSDictionary"), sel.dictionary))
	defer handler.Send(sel.release)
	req := objc.Send[objc.ID](objc.Send[objc.ID](cls("VNRecognizeTextRequest"), sel.alloc), sel.init)
	defer req.Send(sel.release)
	level := int64(0) // accurate
	if opts.Fast {
		level = 1
	}
	objc.Send[objc.ID](req, sel.setLevel, level)
	objc.Send[objc.ID](req, sel.setCorrection, !opts.Fast)
	if len(opts.Languages) > 0 {
		langs := objc.Send[objc.ID](cls("NSMutableArray"), sel.array)
		for _, l := range opts.Languages {
			objc.Send[objc.ID](langs, sel.addObject, objc.Send[objc.ID](cls("NSString"), sel.stringWithUTF8, l))
		}
		objc.Send[objc.ID](req, sel.setLanguages, langs)
	}
	reqs := objc.Send[objc.ID](cls("NSArray"), sel.arrayWithObject, req)
	var nsErr objc.ID
	if ok := objc.Send[bool](handler, sel.perform, reqs, unsafe.Pointer(&nsErr)); !ok {
		return nil, errors.New("ocr: Vision text recognition failed")
	}
	runtime.KeepAlive(data)

	place := func(b cgRect) (x, y, w, h float64) {
		return b.X * pw, (1 - b.Y - b.H) * ph, b.W * pw, b.H * ph
	}
	results := objc.Send[objc.ID](req, sel.results)
	var n uint64
	if results != 0 {
		n = objc.Send[uint64](results, sel.count)
	}
	var out []Line
	for i := uint64(0); i < n; i++ {
		obs := objc.Send[objc.ID](results, sel.objectAtIndex, i)
		cands := objc.Send[objc.ID](obs, sel.topCandidates, uint64(1))
		if cands == 0 || objc.Send[uint64](cands, sel.count) == 0 {
			continue
		}
		rt := objc.Send[objc.ID](cands, sel.objectAtIndex, uint64(0))
		text := goString(objc.Send[uintptr](objc.Send[objc.ID](rt, sel.str), sel.utf8))
		if text == "" {
			continue
		}
		line := Line{Text: text, Confidence: float64(objc.Send[float32](rt, sel.confidence))}
		line.X, line.Y, line.W, line.H = place(objc.Send[cgRect](obs, sel.boundingBox))
		// Words: runs of non-space characters, ranged in UTF-16 units as
		// NSString counts them.
		u := utf16.Encode([]rune(text))
		for start := 0; start < len(u); {
			for start < len(u) && u[start] == ' ' {
				start++
			}
			end := start
			for end < len(u) && u[end] != ' ' {
				end++
			}
			if end > start {
				box := objc.Send[objc.ID](rt, sel.boxForRange, nsRange{uint64(start), uint64(end - start)}, unsafe.Pointer(nil))
				if box != 0 {
					w := Word{Text: string(utf16.Decode(u[start:end]))}
					w.X, w.Y, w.W, w.H = place(objc.Send[cgRect](box, sel.boundingBox))
					line.Words = append(line.Words, w)
				}
			}
			start = end
		}
		out = append(out, line)
	}
	return out, nil
}
