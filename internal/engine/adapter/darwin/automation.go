//go:build darwin

// automation.go — screenshot + synthetic input for the darwin adapter.
//
// Everything is native CoreGraphics/ImageIO through CGO — no
// subprocesses. Capture composites any global rect (including rects
// spanning displays) via CGWindowListCreateImage, downscales retina
// pixels back to point dimensions in a CGBitmapContext, and writes the
// PNG with ImageIO — so one image pixel equals one global coordinate
// point, the invariant the agent visual loop depends on (read pixel,
// click point). CGWindowListCreateImage is deprecated in favor of
// ScreenCaptureKit (Swift/ObjC-async only) but remains the supported
// pure-C path; the pragma silences the deprecation warning.
//
// Input synthesis uses CGEvent posted to the HID event tap. Posting
// requires the same Accessibility trust as Move/Focus, so every input
// entry point runs the silent AX check first and returns
// core.ErrAccessibilityDenied without it. Text is injected via
// CGEventKeyboardSetUnicodeString (layout- and IME-independent);
// chords go through the ANSI virtual-keycode table.
//
// Static C helpers are duplicated per cgo file by design — cgo
// compiles each file as its own translation unit, so adapter.go's
// static wctl_ax_check is not linkable from here; wctl_auto_ax_check
// below is the same three-line call.
package darwin

/*
#cgo CFLAGS: -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework ApplicationServices -framework ImageIO

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <ApplicationServices/ApplicationServices.h>
#include <ImageIO/ImageIO.h>
#include <stdlib.h>
#include <unistd.h>

#pragma clang diagnostic ignored "-Wdeprecated-declarations"

#define WCTL_AUTO_OK        0
#define WCTL_AUTO_DENIED    -1
#define WCTL_AUTO_WRITEFAIL -4
#define WCTL_AUTO_INTERNAL  -5

// Capture the global point rect to a point-normalized PNG at path.
// The compositor hands back pixel-scale data (2x on retina); when the
// pixel size differs from the requested point size we redraw into a
// point-sized bitmap context so the PNG's pixel grid IS the global
// coordinate grid.
static int wctl_capture_rect(double x, double y, double w, double h, const char *path) {
    if (!CGPreflightScreenCaptureAccess()) return WCTL_AUTO_DENIED;
    CGImageRef img = CGWindowListCreateImage(
        CGRectMake(x, y, w, h),
        kCGWindowListOptionOnScreenOnly, kCGNullWindowID, kCGWindowImageDefault);
    if (!img) return WCTL_AUTO_INTERNAL;

    if (CGImageGetWidth(img) != (size_t)w || CGImageGetHeight(img) != (size_t)h) {
        CGColorSpaceRef cs = CGColorSpaceCreateDeviceRGB();
        CGContextRef ctx = CGBitmapContextCreate(
            NULL, (size_t)w, (size_t)h, 8, 0, cs, kCGImageAlphaPremultipliedLast);
        CGColorSpaceRelease(cs);
        if (!ctx) { CGImageRelease(img); return WCTL_AUTO_INTERNAL; }
        CGContextSetInterpolationQuality(ctx, kCGInterpolationHigh);
        CGContextDrawImage(ctx, CGRectMake(0, 0, w, h), img);
        CGImageRef scaled = CGBitmapContextCreateImage(ctx);
        CGContextRelease(ctx);
        CGImageRelease(img);
        if (!scaled) return WCTL_AUTO_INTERNAL;
        img = scaled;
    }

    CFStringRef s = CFStringCreateWithCString(NULL, path, kCFStringEncodingUTF8);
    CFURLRef url = CFURLCreateWithFileSystemPath(NULL, s, kCFURLPOSIXPathStyle, false);
    CFRelease(s);
    if (!url) { CGImageRelease(img); return WCTL_AUTO_INTERNAL; }
    CGImageDestinationRef dest = CGImageDestinationCreateWithURL(url, CFSTR("public.png"), 1, NULL);
    CFRelease(url);
    if (!dest) { CGImageRelease(img); return WCTL_AUTO_INTERNAL; }
    CGImageDestinationAddImage(dest, img, NULL);
    bool ok = CGImageDestinationFinalize(dest);
    CFRelease(dest);
    CGImageRelease(img);
    return ok ? WCTL_AUTO_OK : WCTL_AUTO_WRITEFAIL;
}

// Same silent trust probe as adapter.go's wctl_ax_check (see file
// header for why it is re-stated here).
static int wctl_auto_ax_check(void) {
    CFDictionaryRef opts = CFDictionaryCreate(
        kCFAllocatorDefault,
        (const void **)&kAXTrustedCheckOptionPrompt,
        (const void **)&kCFBooleanFalse,
        1, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    Boolean trusted = AXIsProcessTrustedWithOptions(opts);
    if (opts) CFRelease(opts);
    return trusted ? 1 : 0;
}

static int wctl_screen_capture_check(void) {
    return CGPreflightScreenCaptureAccess() ? 1 : 0;
}

// May surface the system Screen Recording prompt once; returns the
// post-prompt state (0 = still denied, e.g. user must toggle manually
// in System Settings after the first denial).
static int wctl_screen_capture_request(void) {
    return CGRequestScreenCaptureAccess() ? 1 : 0;
}

static void wctl_cursor_pos(double *x, double *y) {
    CGEventRef e = CGEventCreate(NULL);
    CGPoint p = CGEventGetLocation(e);
    if (e) CFRelease(e);
    *x = p.x;
    *y = p.y;
}

static int wctl_mouse_move(double x, double y) {
    CGEventRef e = CGEventCreateMouseEvent(
        NULL, kCGEventMouseMoved, CGPointMake(x, y), kCGMouseButtonLeft);
    if (!e) return WCTL_AUTO_INTERNAL;
    CGEventPost(kCGHIDEventTap, e);
    CFRelease(e);
    return WCTL_AUTO_OK;
}

// button: 0 left, 1 right, 2 middle. clicks: 1 single, 2 double.
// kCGMouseEventClickState must count up across the pairs of a
// multi-click or apps treat it as two separate singles.
static int wctl_mouse_click(double x, double y, int button, int clicks) {
    CGEventType dt, ut;
    CGMouseButton mb;
    switch (button) {
    case 1:  dt = kCGEventRightMouseDown; ut = kCGEventRightMouseUp; mb = kCGMouseButtonRight;  break;
    case 2:  dt = kCGEventOtherMouseDown; ut = kCGEventOtherMouseUp; mb = kCGMouseButtonCenter; break;
    default: dt = kCGEventLeftMouseDown;  ut = kCGEventLeftMouseUp;  mb = kCGMouseButtonLeft;   break;
    }
    CGPoint pt = CGPointMake(x, y);
    CGEventRef move = CGEventCreateMouseEvent(NULL, kCGEventMouseMoved, pt, mb);
    if (move) {
        CGEventPost(kCGHIDEventTap, move);
        CFRelease(move);
    }
    for (int i = 1; i <= clicks; i++) {
        CGEventRef down = CGEventCreateMouseEvent(NULL, dt, pt, mb);
        CGEventRef up   = CGEventCreateMouseEvent(NULL, ut, pt, mb);
        if (!down || !up) {
            if (down) CFRelease(down);
            if (up) CFRelease(up);
            return WCTL_AUTO_INTERNAL;
        }
        CGEventSetIntegerValueField(down, kCGMouseEventClickState, i);
        CGEventSetIntegerValueField(up, kCGMouseEventClickState, i);
        CGEventPost(kCGHIDEventTap, down);
        CGEventPost(kCGHIDEventTap, up);
        CFRelease(down);
        CFRelease(up);
        if (i < clicks) usleep(60000);
    }
    return WCTL_AUTO_OK;
}

// One keyboard down/up pair carrying a literal UTF-16 chunk. Keycode 0
// is ignored by receivers when a unicode string is attached.
static int wctl_type_chunk(const UniChar *chars, int len) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, 0, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(NULL, 0, false);
    if (!down || !up) {
        if (down) CFRelease(down);
        if (up) CFRelease(up);
        return WCTL_AUTO_INTERNAL;
    }
    CGEventKeyboardSetUnicodeString(down, len, chars);
    CGEventKeyboardSetUnicodeString(up, len, chars);
    CGEventPost(kCGHIDEventTap, down);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    return WCTL_AUTO_OK;
}

static int wctl_press_chord(int keycode, int cmd, int ctrl, int alt, int shift) {
    CGEventRef down = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, true);
    CGEventRef up   = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)keycode, false);
    if (!down || !up) {
        if (down) CFRelease(down);
        if (up) CFRelease(up);
        return WCTL_AUTO_INTERNAL;
    }
    CGEventFlags flags = 0;
    if (cmd)   flags |= kCGEventFlagMaskCommand;
    if (ctrl)  flags |= kCGEventFlagMaskControl;
    if (alt)   flags |= kCGEventFlagMaskAlternate;
    if (shift) flags |= kCGEventFlagMaskShift;
    CGEventSetFlags(down, flags);
    CGEventSetFlags(up, flags);
    CGEventPost(kCGHIDEventTap, down);
    usleep(10000);
    CGEventPost(kCGHIDEventTap, up);
    CFRelease(down);
    CFRelease(up);
    return WCTL_AUTO_OK;
}
*/
import "C"

import (
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf16"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
)

func (a *Adapter) CaptureRect(bounds core.Rect, outPath string) error {
	cPath := C.CString(outPath)
	defer C.free(unsafe.Pointer(cPath))
	rc := C.wctl_capture_rect(
		C.double(bounds.X), C.double(bounds.Y),
		C.double(bounds.W), C.double(bounds.H), cPath)
	switch rc {
	case 0:
		return nil
	case C.WCTL_AUTO_DENIED:
		return core.ErrScreenCaptureDenied
	case C.WCTL_AUTO_WRITEFAIL:
		return fmt.Errorf("capture: writing PNG to %s failed", outPath)
	default:
		return fmt.Errorf("capture failed (CoreGraphics rc=%d)", int(rc))
	}
}

func (a *Adapter) CheckScreenCapture() bool {
	return int(C.wctl_screen_capture_check()) == 1
}

func (a *Adapter) RequestScreenCapture() error {
	if int(C.wctl_screen_capture_request()) != 1 {
		return core.ErrScreenCaptureDenied
	}
	return nil
}

// inputAXGuard gates every synthetic-event entry point on the same AX
// trust Move/Focus require, without prompting.
func inputAXGuard() error {
	if int(C.wctl_auto_ax_check()) != 1 {
		return core.ErrAccessibilityDenied
	}
	return nil
}

func (a *Adapter) CursorPosition() (int, int, error) {
	var x, y C.double
	C.wctl_cursor_pos(&x, &y)
	return int(x), int(y), nil
}

func (a *Adapter) MouseMove(x, y int) error {
	if err := inputAXGuard(); err != nil {
		return err
	}
	if rc := C.wctl_mouse_move(C.double(x), C.double(y)); rc != 0 {
		return fmt.Errorf("mouse move failed (CGEvent rc=%d)", int(rc))
	}
	return nil
}

func (a *Adapter) MouseClick(x, y int, button core.MouseButton, clicks int) error {
	if err := inputAXGuard(); err != nil {
		return err
	}
	if rc := C.wctl_mouse_click(C.double(x), C.double(y), C.int(button), C.int(clicks)); rc != 0 {
		return fmt.Errorf("mouse click failed (CGEvent rc=%d)", int(rc))
	}
	return nil
}

// typeChunkSize is the number of UTF-16 units carried per keyboard
// event pair. CGEventKeyboardSetUnicodeString accepts more, but small
// chunks with a breather between them keep slow apps (Electron,
// terminals) from dropping characters.
const typeChunkSize = 20

func (a *Adapter) TypeText(text string) error {
	if err := inputAXGuard(); err != nil {
		return err
	}
	units := utf16.Encode([]rune(text))
	for start := 0; start < len(units); start += typeChunkSize {
		end := start + typeChunkSize
		if end > len(units) {
			end = len(units)
		}
		chunk := units[start:end]
		rc := C.wctl_type_chunk(
			(*C.UniChar)(unsafe.Pointer(&chunk[0])),
			C.int(len(chunk)))
		if rc != 0 {
			return fmt.Errorf("type text failed (CGEvent rc=%d)", int(rc))
		}
		C.usleep(10000)
	}
	return nil
}

// darwinKeycodes maps the normalized key names from core.Chord to ANSI
// (US layout) virtual keycodes. Non-letter punctuation is positional:
// on other layouts the glyph produced may differ — documented in the
// skill reference.
var darwinKeycodes = map[string]int{
	"a": 0, "s": 1, "d": 2, "f": 3, "h": 4, "g": 5, "z": 6, "x": 7,
	"c": 8, "v": 9, "b": 11, "q": 12, "w": 13, "e": 14, "r": 15,
	"y": 16, "t": 17, "1": 18, "2": 19, "3": 20, "4": 21, "6": 22,
	"5": 23, "=": 24, "9": 25, "7": 26, "-": 27, "8": 28, "0": 29,
	"]": 30, "o": 31, "u": 32, "[": 33, "i": 34, "p": 35,
	"enter": 36, "l": 37, "j": 38, "'": 39, "k": 40, ";": 41,
	"\\": 42, ",": 43, "/": 44, "n": 45, "m": 46, ".": 47,
	"tab": 48, "space": 49, "`": 50, "backspace": 51, "esc": 53,
	"f1": 122, "f2": 120, "f3": 99, "f4": 118, "f5": 96, "f6": 97,
	"f7": 98, "f8": 100, "f9": 101, "f10": 109, "f11": 103, "f12": 111,
	"home": 115, "pageup": 116, "delete": 117, "end": 119,
	"pagedown": 121, "left": 123, "right": 124, "down": 125, "up": 126,
}

func (a *Adapter) PressChord(chord core.Chord) error {
	if err := inputAXGuard(); err != nil {
		return err
	}
	code, ok := darwinKeycodes[chord.Key]
	if !ok {
		return fmt.Errorf("key %q has no macOS keycode mapping", chord.Key)
	}
	rc := C.wctl_press_chord(C.int(code),
		boolToC(chord.Cmd), boolToC(chord.Ctrl), boolToC(chord.Alt), boolToC(chord.Shift))
	if rc != 0 {
		return fmt.Errorf("key press failed (CGEvent rc=%d)", int(rc))
	}
	return nil
}

func boolToC(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

func (a *Adapter) Launch(app string) error {
	out, err := exec.Command("/usr/bin/open", "-a", app).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launch %q failed: %v: %s", app, err, strings.TrimSpace(string(out)))
	}
	return nil
}
