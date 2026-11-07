package groups

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <dlfcn.h>
#include <CoreGraphics/CoreGraphics.h>

// DisplayServices is the private framework System Settings and the brightness
// keys use; it is the route that works on Apple Silicon panels. It is loaded
// with dlopen, so a macOS without it degrades to an error, not a crash.
typedef int (*aosGetFn)(CGDirectDisplayID, float *);
typedef int (*aosSetFn)(CGDirectDisplayID, float);

static void *aosDisplayServices(void) {
	static void *h;
	if (!h) h = dlopen("/System/Library/PrivateFrameworks/DisplayServices.framework/DisplayServices", RTLD_LAZY);
	return h;
}

// aosBrightnessDisplay is the built-in panel when there is one (external
// monitors rarely take software brightness), otherwise the main display.
static CGDirectDisplayID aosBrightnessDisplay(void) {
	CGDirectDisplayID ids[16];
	uint32_t n = 0;
	if (CGGetOnlineDisplayList(16, ids, &n) == kCGErrorSuccess) {
		for (uint32_t i = 0; i < n; i++) {
			if (CGDisplayIsBuiltin(ids[i])) return ids[i];
		}
	}
	return CGMainDisplayID();
}

static int aosGetBrightness(float *out) {
	void *h = aosDisplayServices();
	if (!h) return -1;
	aosGetFn f = (aosGetFn)dlsym(h, "DisplayServicesGetBrightness");
	if (!f) return -1;
	return f(aosBrightnessDisplay(), out);
}

static int aosSetBrightness(float v) {
	void *h = aosDisplayServices();
	if (!h) return -1;
	aosSetFn f = (aosSetFn)dlsym(h, "DisplayServicesSetBrightness");
	if (!f) return -1;
	return f(aosBrightnessDisplay(), v);
}
*/
import "C"

import (
	"math"

	"github.com/muthuishere/aos/internal/cli"
)

var errNoBrightness = &cli.ExitError{Code: 2, Message: "the main display does not expose brightness control (external monitors usually do not)"}

func getBrightness() (int, error) {
	var v C.float
	if C.aosGetBrightness(&v) != 0 {
		return 0, errNoBrightness
	}
	return int(math.Round(float64(v) * 100)), nil
}

func setBrightness(pct int) error {
	if C.aosSetBrightness(C.float(float64(pct)/100)) != 0 {
		return errNoBrightness
	}
	return nil
}
