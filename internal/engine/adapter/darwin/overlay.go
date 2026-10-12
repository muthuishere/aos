//go:build darwin

package darwin

/*
#cgo CFLAGS: -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework AppKit -framework QuartzCore

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <objc/runtime.h>
#include <objc/message.h>
#include <pthread.h>
#include <stdlib.h>
#include <string.h>

typedef struct objc_object* objid;

extern void *objc_autoreleasePoolPush(void);
extern void objc_autoreleasePoolPop(void *);

// Typed objc_msgSend casts (calling the untyped variadic symbol directly
// is undefined on arm64).
#define MSG(ret, ...) ((ret (*)(objid, SEL, ##__VA_ARGS__))objc_msgSend)
static SEL S(const char *n) { return sel_registerName(n); }
static objid C(const char *n) { return (objid)objc_getClass(n); }

// Backing scale (pixels per point) of the display under the rect's centre.
static double wctl_overlay_scale(double x, double y, double w, double h) {
    CGPoint c = CGPointMake(x + w / 2, y + h / 2);
    CGDirectDisplayID ids[8];
    uint32_t n = 0;
    if (CGGetDisplaysWithPoint(c, 8, ids, &n) != kCGErrorSuccess || n == 0) {
        ids[0] = CGMainDisplayID();
    }
    CGDisplayModeRef mode = CGDisplayCopyDisplayMode(ids[0]);
    if (!mode) return 1;
    double pw = (double)CGDisplayModeGetPixelWidth(mode);
    double lw = (double)CGDisplayModeGetWidth(mode);
    CGDisplayModeRelease(mode);
    return lw > 0 ? pw / lw : 1;
}

static int wctl_is_main_thread(void) { return pthread_main_np(); }

// Shows a transparent, click-through, never-key panel over the global
// top-left rect (x,y,w,h in points) holding the premultiplied RGBA
// image. Returns the panel (retained) or NULL.
static void wctl_overlay_set_image(void *p, const unsigned char *rgba, int pw, int ph, double w);

static void *wctl_overlay_show(double x, double y, double w, double h,
                               const unsigned char *rgba, int pw, int ph, double alpha) {
    void *pool = objc_autoreleasePoolPush();
    objid app = MSG(objid)(C("NSApplication"), S("sharedApplication"));
    // Accessory: no Dock icon, no menu bar, never steals activation, and
    // (with a non-activating panel) allowed over other apps' fullscreen Spaces.
    MSG(signed char, long)(app, S("setActivationPolicy:"), 1L);
    MSG(void)(app, S("finishLaunching"));

    // AppKit frames are bottom-left, relative to the primary display.
    double primaryH = CGDisplayBounds(CGMainDisplayID()).size.height;
    CGRect frame = CGRectMake(x, primaryH - (y + h), w, h);
    unsigned long style = 0 | (1UL << 7); // borderless | NSWindowStyleMaskNonactivatingPanel
    objid panel = MSG(objid)(C("NSPanel"), S("alloc"));
    panel = MSG(objid, CGRect, unsigned long, unsigned long, signed char)(
        panel, S("initWithContentRect:styleMask:backing:defer:"), frame, style, 2UL, (signed char)0);
    if (!panel) { objc_autoreleasePoolPop(pool); return NULL; }

    MSG(void, signed char)(panel, S("setOpaque:"), 0);
    MSG(void, objid)(panel, S("setBackgroundColor:"), MSG(objid)(C("NSColor"), S("clearColor")));
    MSG(void, signed char)(panel, S("setHasShadow:"), 0);
    MSG(void, signed char)(panel, S("setIgnoresMouseEvents:"), 1);
    MSG(void, signed char)(panel, S("setHidesOnDeactivate:"), 0);
    MSG(void, signed char)(panel, S("setReleasedWhenClosed:"), 0);
    MSG(void, signed char)(panel, S("setBecomesKeyOnlyIfNeeded:"), 1);
    MSG(void, long)(panel, S("setLevel:"), (long)CGWindowLevelForKey(kCGScreenSaverWindowLevelKey));
    // canJoinAllSpaces | stationary | ignoresCycle | fullScreenAuxiliary
    MSG(void, unsigned long)(panel, S("setCollectionBehavior:"), 1UL | 16UL | 64UL | 256UL);
    // Keep the image exactly where the frame says, even across displays.
    MSG(void, CGRect, signed char)(panel, S("setFrame:display:"), frame, (signed char)0);

    MSG(void, double)(panel, S("setAlphaValue:"), alpha);
    wctl_overlay_set_image(panel, rgba, pw, ph, w);

    MSG(void)(panel, S("orderFrontRegardless"));
    objc_autoreleasePoolPop(pool);
    return panel;
}

// Replaces the panel's picture (w = its width in points). A view's
// backing layer has no implicit animations, so the swap is instant.
static void wctl_overlay_set_image(void *p, const unsigned char *rgba, int pw, int ph, double w) {
    void *pool = objc_autoreleasePoolPush();
    objid panel = (objid)p;
    CGColorSpaceRef cs = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    CFDataRef data = CFDataCreate(NULL, rgba, (CFIndex)pw * ph * 4);
    CGDataProviderRef dp = CGDataProviderCreateWithCFData(data);
    CGImageRef img = CGImageCreate(pw, ph, 8, 32, (size_t)pw * 4, cs,
        kCGImageAlphaPremultipliedLast | kCGBitmapByteOrderDefault, dp, NULL, 0, kCGRenderingIntentDefault);
    CGDataProviderRelease(dp);
    CFRelease(data);
    CGColorSpaceRelease(cs);

    objid view = MSG(objid)(panel, S("contentView"));
    MSG(void, signed char)(view, S("setWantsLayer:"), 1);
    objid layer = MSG(objid)(view, S("layer"));
    objid tx = C("CATransaction");
    MSG(void)(tx, S("begin"));
    MSG(void, signed char)(tx, S("setDisableActions:"), 1);
    MSG(void, objid)(layer, S("setContents:"), (objid)img);
    MSG(void, double)(layer, S("setContentsScale:"), w > 0 ? (double)pw / w : 1.0);
    MSG(void)(tx, S("commit"));
    CGImageRelease(img);

    objc_autoreleasePoolPop(pool);
}

static void wctl_overlay_set_alpha(void *p, double alpha) {
    void *pool = objc_autoreleasePoolPush();
    MSG(void, double)((objid)p, S("setAlphaValue:"), alpha);
    objc_autoreleasePoolPop(pool);
}

// System Settings > Accessibility > Display > Reduce motion.
static int wctl_reduce_motion(void) {
    void *pool = objc_autoreleasePoolPush();
    objid ws = MSG(objid)(C("NSWorkspace"), S("sharedWorkspace"));
    int r = MSG(signed char)(ws, S("accessibilityDisplayShouldReduceMotion")) ? 1 : 0;
    objc_autoreleasePoolPop(pool);
    return r;
}

// Pumps the AppKit event loop for up to `seconds`.
static void wctl_overlay_pump(double seconds) {
    void *pool = objc_autoreleasePoolPush();
    objid app = MSG(objid)(C("NSApplication"), S("sharedApplication"));
    objid until = MSG(objid, double)(C("NSDate"), S("dateWithTimeIntervalSinceNow:"), seconds);
    objid ev = MSG(objid, unsigned long long, objid, objid, signed char)(
        app, S("nextEventMatchingMask:untilDate:inMode:dequeue:"),
        ~0ULL, until, (objid)kCFRunLoopDefaultMode, (signed char)1);
    if (ev) MSG(void, objid)(app, S("sendEvent:"), ev);
    objc_autoreleasePoolPop(pool);
}

static void wctl_overlay_close(void *p) {
    if (!p) return;
    void *pool = objc_autoreleasePoolPush();
    objid panel = (objid)p;
    MSG(void, objid)(panel, S("orderOut:"), (objid)0);
    MSG(void)(panel, S("close"));
    MSG(void)(panel, S("release"));
    objc_autoreleasePoolPop(pool);
    wctl_overlay_pump(0); // let the window server drop it before we return
}
*/
import "C"

import (
	"errors"
	"image"
	"runtime"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
)

// AppKit windows may only be created on the main thread. Locking the
// main goroutine to it in init (which runs on the main thread) is the
// standard Go pattern; ShowOverlay then works when called from main.
func init() { runtime.LockOSThread() }

func (a *Adapter) OverlayScale(b core.Rect) (float64, float64) {
	return 1, float64(C.wctl_overlay_scale(C.double(b.X), C.double(b.Y), C.double(b.W), C.double(b.H)))
}

func (a *Adapter) ShowOverlay(b core.Rect, img *image.RGBA, done func() bool) error {
	if C.wctl_is_main_thread() == 0 {
		return errors.New("draw: on macOS the overlay must be shown from the main goroutine (AppKit owns the main thread)")
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 {
		return errors.New("draw: empty overlay image")
	}
	panel := C.wctl_overlay_show(C.double(b.X), C.double(b.Y), C.double(b.W), C.double(b.H),
		(*C.uchar)(unsafe.Pointer(&img.Pix[0])), C.int(w), C.int(h), 1)
	if panel == nil {
		return errors.New("draw: could not create the overlay window")
	}
	defer C.wctl_overlay_close(panel)
	for !done() {
		C.wctl_overlay_pump(0.05)
	}
	return nil
}

// ---- animated overlays (core.OverlayAnimator) ----

var _ core.OverlayAnimator = (*Adapter)(nil)

type overlay struct {
	panel unsafe.Pointer
	w, h  int
	b     core.Rect
}

func (a *Adapter) OpenOverlay(b core.Rect, img *image.RGBA, opacity float64) (core.Overlay, error) {
	if C.wctl_is_main_thread() == 0 {
		return nil, errors.New("draw: on macOS the overlay must be shown from the main goroutine (AppKit owns the main thread)")
	}
	w, h := img.Rect.Dx(), img.Rect.Dy()
	if w == 0 || h == 0 {
		return nil, errors.New("draw: empty overlay image")
	}
	panel := C.wctl_overlay_show(C.double(b.X), C.double(b.Y), C.double(b.W), C.double(b.H),
		(*C.uchar)(unsafe.Pointer(&img.Pix[0])), C.int(w), C.int(h), C.double(opacity))
	if panel == nil {
		return nil, errors.New("draw: could not create the overlay window")
	}
	return &overlay{panel: panel, w: w, h: h, b: b}, nil
}

func (o *overlay) SetImage(img *image.RGBA) error {
	if img.Rect.Dx() != o.w || img.Rect.Dy() != o.h || img.Stride != o.w*4 {
		return errors.New("draw: overlay frame size changed")
	}
	C.wctl_overlay_set_image(o.panel, (*C.uchar)(unsafe.Pointer(&img.Pix[0])), C.int(o.w), C.int(o.h), C.double(o.b.W))
	return nil
}

func (o *overlay) SetOpacity(v float64) { C.wctl_overlay_set_alpha(o.panel, C.double(v)) }

func (o *overlay) Close() {
	if o.panel != nil {
		C.wctl_overlay_close(o.panel)
		o.panel = nil
	}
}

// PumpOverlays runs the event loop until d has passed (an event can end
// one wait early, so keep waiting until the deadline).
func (a *Adapter) PumpOverlays(d time.Duration) {
	end := time.Now().Add(d)
	for {
		left := time.Until(end)
		if left <= 0 {
			return
		}
		C.wctl_overlay_pump(C.double(left.Seconds()))
	}
}

func (a *Adapter) ReduceMotion() bool { return C.wctl_reduce_motion() != 0 }
