//go:build darwin

package darwin

/*
#cgo CFLAGS: -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework Foundation -framework Vision

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <objc/runtime.h>
#include <objc/message.h>
#include <stdlib.h>
#include <string.h>

typedef struct objc_object* ocrid;
typedef struct { unsigned long location, length; } ocr_range;

extern void *objc_autoreleasePoolPush(void);
extern void objc_autoreleasePoolPop(void *);

#define OMSG(ret, ...) ((ret (*)(ocrid, SEL, ##__VA_ARGS__))objc_msgSend)
static SEL OS(const char *n) { return sel_registerName(n); }
static ocrid OC(const char *n) { return (ocrid)objc_getClass(n); }

// CGRect-returning messages need the _stret entry point on x86_64.
static CGRect ocr_rect(ocrid obj, SEL sel) {
#if defined(__x86_64__)
    return ((CGRect (*)(ocrid, SEL))objc_msgSend_stret)(obj, sel);
#else
    return ((CGRect (*)(ocrid, SEL))objc_msgSend)(obj, sel);
#endif
}

typedef struct {
    char* text;
    int line;      // index of the line this word belongs to; -1 for a line
    double x, y, w, h;
} wctl_ocr_t;

static char* ocr_dup(ocrid nsstr) {
    if (!nsstr) return NULL;
    const char* u = OMSG(const char*)(nsstr, OS("UTF8String"));
    return u ? strdup(u) : NULL;
}

// Converts Vision's normalized bottom-left box to global top-left points.
static void ocr_place(CGRect b, double rx, double ry, double rw, double rh, wctl_ocr_t* t) {
    t->x = rx + b.origin.x * rw;
    t->y = ry + (1.0 - b.origin.y - b.size.height) * rh;
    t->w = b.size.width * rw;
    t->h = b.size.height * rh;
}

// wctl_ocr captures the global rect (points) at the display's native
// resolution and runs VNRecognizeTextRequest (accurate). Emits each line
// followed by its words, each word boxed via boundingBoxForRange:.
// Returns the item count, or -1 when the capture fails (no Screen
// Recording grant), -2 when Vision fails.
static int wctl_ocr(double rx, double ry, double rw, double rh, wctl_ocr_t** out) {
    *out = NULL;
    CGImageRef img = CGWindowListCreateImage(CGRectMake(rx, ry, rw, rh),
        kCGWindowListOptionOnScreenOnly, kCGNullWindowID, kCGWindowImageBestResolution);
    if (!img) return -1;
    void* pool = objc_autoreleasePoolPush();
    ocrid handler = OMSG(ocrid)(OC("VNImageRequestHandler"), OS("alloc"));
    handler = OMSG(ocrid, CGImageRef, ocrid)(handler, OS("initWithCGImage:options:"), img,
        OMSG(ocrid)(OC("NSDictionary"), OS("dictionary")));
    ocrid req = OMSG(ocrid)(OMSG(ocrid)(OC("VNRecognizeTextRequest"), OS("alloc")), OS("init"));
    OMSG(void, long)(req, OS("setRecognitionLevel:"), 0L);          // accurate
    OMSG(void, signed char)(req, OS("setUsesLanguageCorrection:"), 1);
    ocrid reqs = OMSG(ocrid, ocrid)(OC("NSArray"), OS("arrayWithObject:"), req);
    ocrid err = NULL;
    signed char ok = OMSG(signed char, ocrid, ocrid*)(handler, OS("performRequests:error:"), reqs, &err);
    CGImageRelease(img);
    if (!ok) {
        OMSG(void)(req, OS("release"));
        OMSG(void)(handler, OS("release"));
        objc_autoreleasePoolPop(pool);
        return -2;
    }
    ocrid results = OMSG(ocrid)(req, OS("results"));
    unsigned long count = results ? OMSG(unsigned long)(results, OS("count")) : 0;
    int cap = 64, n = 0;
    wctl_ocr_t* items = (wctl_ocr_t*)malloc(sizeof(wctl_ocr_t) * cap);
    for (unsigned long i = 0; i < count; i++) {
        ocrid obs = OMSG(ocrid, unsigned long)(results, OS("objectAtIndex:"), i);
        ocrid cands = OMSG(ocrid, unsigned long)(obs, OS("topCandidates:"), 1UL);
        if (!cands || OMSG(unsigned long)(cands, OS("count")) == 0) continue;
        ocrid rt = OMSG(ocrid, unsigned long)(cands, OS("objectAtIndex:"), 0UL);
        ocrid str = OMSG(ocrid)(rt, OS("string"));
        unsigned long len = OMSG(unsigned long)(str, OS("length"));
        if (n + 1 + (int)len >= cap) {
            cap = (n + 1 + (int)len) * 2;
            items = (wctl_ocr_t*)realloc(items, sizeof(wctl_ocr_t) * cap);
        }
        int lineIdx = n;
        wctl_ocr_t* L = &items[n++];
        L->text = ocr_dup(str);
        L->line = -1;
        ocr_place(ocr_rect(obs, OS("boundingBox")), rx, ry, rw, rh, L);
        // Words: maximal runs of non-space UTF-16 units.
        unsigned long start = 0;
        while (start < len) {
            while (start < len && OMSG(unsigned short, unsigned long)(str, OS("characterAtIndex:"), start) == ' ') start++;
            unsigned long end = start;
            while (end < len && OMSG(unsigned short, unsigned long)(str, OS("characterAtIndex:"), end) != ' ') end++;
            if (end > start) {
                ocr_range r = {start, end - start};
                ocrid box = OMSG(ocrid, ocr_range, ocrid*)(rt, OS("boundingBoxForRange:error:"), r, NULL);
                if (box) {
                    wctl_ocr_t* W = &items[n++];
                    W->text = ocr_dup(OMSG(ocrid, ocr_range)(str, OS("substringWithRange:"), r));
                    W->line = lineIdx;
                    ocr_place(ocr_rect(box, OS("boundingBox")), rx, ry, rw, rh, W);
                }
            }
            start = end;
        }
    }
    OMSG(void)(req, OS("release"));
    OMSG(void)(handler, OS("release"));
    objc_autoreleasePoolPop(pool);
    *out = items;
    return n;
}

static void wctl_free_ocr(wctl_ocr_t* t, int n) {
    for (int i = 0; i < n; i++) free(t[i].text);
    free(t);
}
*/
import "C"

import (
	"errors"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
)

// RecognizeText OCRs a global rect with Apple's Vision framework (on
// device, no network). Needs the Screen Recording grant.
func (a *Adapter) RecognizeText(b core.Rect) ([]core.TextItem, error) {
	if !a.CheckScreenCapture() {
		return nil, core.ErrScreenCaptureDenied
	}
	var items *C.wctl_ocr_t
	n := int(C.wctl_ocr(C.double(b.X), C.double(b.Y), C.double(b.W), C.double(b.H), &items))
	switch {
	case n == -1:
		return nil, core.ErrScreenCaptureDenied
	case n < 0:
		return nil, errors.New("windowctl darwin: Vision text recognition failed")
	case items == nil:
		return nil, nil
	}
	defer C.wctl_free_ocr(items, C.int(n))
	var out []core.TextItem
	for _, t := range unsafe.Slice(items, n) {
		it := core.TextItem{
			Text:   cStringOrEmpty(t.text),
			Bounds: core.Rect{X: int(t.x), Y: int(t.y), W: int(t.w + 0.5), H: int(t.h + 0.5)},
			Source: "ocr", Role: "text",
		}
		if t.line < 0 {
			out = append(out, it)
		} else if len(out) > 0 {
			out[len(out)-1].Words = append(out[len(out)-1].Words, it)
		}
	}
	return out, nil
}
