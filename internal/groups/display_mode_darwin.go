package groups

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

static CGDirectDisplayID aosDisplayAt(int x, int y) {
	CGDirectDisplayID ids[16]; uint32_t n = 0;
	if (CGGetDisplaysWithPoint(CGPointMake(x + 1, y + 1), 16, ids, &n) != kCGErrorSuccess || n == 0) return 0;
	return ids[0];
}

// aosModes fills up to max modes as (w, h, hz*100, current) and returns the count.
static int aosModes(CGDirectDisplayID d, int *out, int max) {
	CFArrayRef modes = CGDisplayCopyAllDisplayModes(d, NULL);
	if (!modes) return 0;
	CGDisplayModeRef cur = CGDisplayCopyDisplayMode(d);
	int n = 0;
	for (CFIndex i = 0; i < CFArrayGetCount(modes) && n < max; i++) {
		CGDisplayModeRef m = (CGDisplayModeRef)CFArrayGetValueAtIndex(modes, i);
		if (!CGDisplayModeIsUsableForDesktopGUI(m)) continue;
		out[n*4] = (int)CGDisplayModeGetWidth(m);
		out[n*4+1] = (int)CGDisplayModeGetHeight(m);
		out[n*4+2] = (int)(CGDisplayModeGetRefreshRate(m) * 100);
		out[n*4+3] = cur && CGDisplayModeGetIODisplayModeID(m) == CGDisplayModeGetIODisplayModeID(cur);
		n++;
	}
	if (cur) CGDisplayModeRelease(cur);
	CFRelease(modes);
	return n;
}

static int aosSetMode(CGDirectDisplayID d, int w, int h, int hz100) {
	CFArrayRef modes = CGDisplayCopyAllDisplayModes(d, NULL);
	if (!modes) return -1;
	CGDisplayModeRef pick = NULL;
	for (CFIndex i = 0; i < CFArrayGetCount(modes); i++) {
		CGDisplayModeRef m = (CGDisplayModeRef)CFArrayGetValueAtIndex(modes, i);
		if (CGDisplayModeIsUsableForDesktopGUI(m) && (int)CGDisplayModeGetWidth(m) == w &&
		    (int)CGDisplayModeGetHeight(m) == h && (int)(CGDisplayModeGetRefreshRate(m) * 100) == hz100) { pick = m; break; }
	}
	int rc = -2;
	CGDisplayConfigRef cfg;
	if (pick && CGBeginDisplayConfiguration(&cfg) == kCGErrorSuccess) {
		CGConfigureDisplayWithDisplayMode(cfg, d, pick, NULL);
		rc = CGCompleteDisplayConfiguration(cfg, kCGConfigureForSession);
	}
	CFRelease(modes);
	return rc;
}

static int aosMirror(int on) {
	CGDirectDisplayID ids[16]; uint32_t n = 0;
	if (CGGetOnlineDisplayList(16, ids, &n) != kCGErrorSuccess) return -1;
	if (n < 2) return -3;
	CGDirectDisplayID main = CGMainDisplayID();
	CGDisplayConfigRef cfg;
	if (CGBeginDisplayConfiguration(&cfg) != kCGErrorSuccess) return -1;
	for (uint32_t i = 0; i < n; i++) {
		if (ids[i] == main) continue;
		CGConfigureDisplayMirrorOfDisplay(cfg, ids[i], on ? main : kCGNullDirectDisplay);
	}
	return CGCompleteDisplayConfiguration(cfg, kCGConfigureForSession);
}
*/
import "C"

import (
	"fmt"

	"github.com/muthuishere/aos/internal/engine"
)

func cgDisplay(mon engine.Monitor) (C.CGDirectDisplayID, error) {
	d := C.aosDisplayAt(C.int(mon.X), C.int(mon.Y))
	if d == 0 {
		return 0, fmt.Errorf("no display at %d,%d", mon.X, mon.Y)
	}
	return d, nil
}

func listDisplayModes(mon engine.Monitor) ([]displayMode, error) {
	d, err := cgDisplay(mon)
	if err != nil {
		return nil, err
	}
	buf := make([]C.int, 4*512)
	n := int(C.aosModes(d, &buf[0], 512))
	out := make([]displayMode, n)
	for i := range out {
		out[i] = displayMode{Width: int(buf[i*4]), Height: int(buf[i*4+1]), Refresh: float64(buf[i*4+2]) / 100, Current: buf[i*4+3] != 0}
	}
	return out, nil
}

// setDisplayMode applies for this login session only (kCGConfigureForSession).
func setDisplayMode(mon engine.Monitor, m displayMode) error {
	d, err := cgDisplay(mon)
	if err != nil {
		return err
	}
	if rc := C.aosSetMode(d, C.int(m.Width), C.int(m.Height), C.int(m.Refresh*100+0.5)); rc != 0 {
		return fmt.Errorf("macOS refused the mode change (CoreGraphics error %d)", int(rc))
	}
	return nil
}

func setMirror(on bool) error {
	b := 0
	if on {
		b = 1
	}
	switch rc := C.aosMirror(C.int(b)); rc {
	case 0:
		return nil
	case -3:
		return fmt.Errorf("mirroring needs two monitors")
	default:
		return fmt.Errorf("macOS refused the mirroring change (CoreGraphics error %d)", int(rc))
	}
}
