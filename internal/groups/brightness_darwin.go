package groups

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework IOKit -framework AppKit
#import <AppKit/AppKit.h>
#include <CoreGraphics/CoreGraphics.h>
#include <IOKit/IOKitLib.h>
#include <dlfcn.h>
#include <string.h>
#include <unistd.h>

// DisplayServices is the private framework System Settings and the brightness
// keys use for the built-in panel. It is loaded with dlopen, so a macOS
// without it degrades to an error, not a crash.
typedef int (*aosGetFn)(CGDirectDisplayID, float *);
typedef int (*aosSetFn)(CGDirectDisplayID, float);

static void *aosDisplayServices(void) {
	static void *h;
	if (!h) h = dlopen("/System/Library/PrivateFrameworks/DisplayServices.framework/DisplayServices", RTLD_LAZY);
	return h;
}

static int aosGetBrightness(CGDirectDisplayID d, float *out) {
	void *h = aosDisplayServices();
	aosGetFn f = h ? (aosGetFn)dlsym(h, "DisplayServicesGetBrightness") : NULL;
	return f ? f(d, out) : -1;
}

static int aosSetBrightness(CGDirectDisplayID d, float v) {
	void *h = aosDisplayServices();
	aosSetFn f = h ? (aosSetFn)dlsym(h, "DisplayServicesSetBrightness") : NULL;
	return f ? f(d, v) : -1;
}

static int aosIsBuiltin(CGDirectDisplayID d) { return CGDisplayIsBuiltin(d); }
static int aosIsAsleep(CGDirectDisplayID d) { return CGDisplayIsAsleep(d); }

// aosScreenName copies the name macOS shows in System Settings
// (NSScreen.localizedName, e.g. "BenQ GW2785TC" or "Built-in Retina Display").
static int aosScreenName(CGDirectDisplayID d, char *buf, int n) {
	@autoreleasepool {
		for (NSScreen *s in [NSScreen screens]) {
			NSNumber *num = s.deviceDescription[@"NSScreenNumber"];
			if (num.unsignedIntValue != d) continue;
			NSString *name = nil;
			if ([s respondsToSelector:@selector(localizedName)]) name = s.localizedName;
			if (name && [name getCString:buf maxLength:n encoding:NSUTF8StringEncoding]) return 0;
		}
	}
	return -1;
}

// IOAVService is the private IOKit API Apple Silicon exposes for I2C to an
// external display; DDC/CI rides on it (the same route m1ddc and
// MonitorControl use). Resolved with dlsym, so its absence is an error.
typedef CFTypeRef (*aosAVCreateFn)(CFAllocatorRef, io_service_t);
typedef IOReturn (*aosAVI2CFn)(CFTypeRef, uint32_t, uint32_t, void *, uint32_t);

static void *aosIOKit(void) {
	static void *h;
	if (!h) h = dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", RTLD_LAZY);
	return h;
}

static int aosDictInt(CFDictionaryRef d, CFStringRef key) {
	CFNumberRef n = d ? CFDictionaryGetValue(d, key) : NULL;
	int v = -1;
	if (n && CFGetTypeID(n) == CFNumberGetTypeID()) CFNumberGetValue(n, kCFNumberIntType, &v);
	return v;
}

typedef struct { int vendor, product, serial; CFTypeRef svc; } aosAV;

// aosFindAV walks the service plane. Each external display's framebuffer
// (AppleCLCD2 / IOMobileFramebufferShim) comes just before its
// DCPAVServiceProxy, so the framebuffer's EDID identity tags the proxy. The
// proxy matching vendor/product/serial (or vendor/product when unique) wins.
static CFTypeRef aosFindAV(int vendor, int product, int serial) {
	void *h = aosIOKit();
	aosAVCreateFn create = h ? (aosAVCreateFn)dlsym(h, "IOAVServiceCreateWithService") : NULL;
	if (!create) return NULL;
	io_iterator_t it;
	if (IORegistryEntryCreateIterator(IORegistryGetRootEntry(kIOMainPortDefault), kIOServicePlane,
	                                  kIORegistryIterateRecursively, &it) != KERN_SUCCESS) return NULL;
	aosAV found[16]; int n = 0;
	int cv = -1, cp = -1, cs = -1;
	io_service_t e;
	while ((e = IOIteratorNext(it)) != 0) {
		io_name_t cls;
		if (IOObjectGetClass(e, cls) == KERN_SUCCESS) {
			if (!strcmp(cls, "AppleCLCD2") || !strcmp(cls, "IOMobileFramebufferShim")) {
				cv = cp = cs = -1;
				CFTypeRef attrs = IORegistryEntryCreateCFProperty(e, CFSTR("DisplayAttributes"), kCFAllocatorDefault, 0);
				if (attrs && CFGetTypeID(attrs) == CFDictionaryGetTypeID()) {
					CFDictionaryRef pa = CFDictionaryGetValue(attrs, CFSTR("ProductAttributes"));
					if (pa && CFGetTypeID(pa) == CFDictionaryGetTypeID()) {
						cv = aosDictInt(pa, CFSTR("LegacyManufacturerID"));
						cp = aosDictInt(pa, CFSTR("ProductID"));
						cs = aosDictInt(pa, CFSTR("SerialNumber"));
					}
				}
				if (attrs) CFRelease(attrs);
			} else if (!strcmp(cls, "DCPAVServiceProxy") && n < 16) {
				CFTypeRef loc = IORegistryEntryCreateCFProperty(e, CFSTR("Location"), kCFAllocatorDefault, 0);
				int external = loc && CFGetTypeID(loc) == CFStringGetTypeID() &&
				               CFStringCompare(loc, CFSTR("External"), 0) == kCFCompareEqualTo;
				if (loc) CFRelease(loc);
				if (external) {
					CFTypeRef svc = create(kCFAllocatorDefault, e);
					if (svc) found[n++] = (aosAV){cv, cp, cs, svc};
				}
			}
		}
		IOObjectRelease(e);
	}
	IOObjectRelease(it);
	CFTypeRef pick = NULL;
	int same = 0;
	for (int i = 0; i < n; i++) {
		if (found[i].vendor == vendor && found[i].product == product) {
			same++;
			if (found[i].serial == serial || !pick) pick = found[i].svc;
		}
	}
	if (same > 1) {
		// Two identical models: only an exact serial match is safe.
		CFTypeRef exact = NULL;
		for (int i = 0; i < n; i++)
			if (found[i].vendor == vendor && found[i].product == product && found[i].serial == serial) exact = found[i].svc;
		pick = exact;
	}
	for (int i = 0; i < n; i++) if (found[i].svc != pick) CFRelease(found[i].svc);
	return pick;
}

static CFTypeRef aosDDCService(CGDirectDisplayID d) {
	return aosFindAV((int)CGDisplayVendorNumber(d), (int)CGDisplayModelNumber(d), (int)CGDisplaySerialNumber(d));
}

static IOReturn aosI2C(CFTypeRef svc, int write, uint8_t *buf, uint32_t n) {
	void *h = aosIOKit();
	aosAVI2CFn f = h ? (aosAVI2CFn)dlsym(h, write ? "IOAVServiceWriteI2C" : "IOAVServiceReadI2C") : NULL;
	return f ? f(svc, 0x37, 0x51, buf, n) : -1;
}

// aosDDCSet sends "Set VCP Feature": 0x84 0x03 vcp hi lo checksum.
static int aosDDCSet(CFTypeRef svc, uint8_t vcp, uint16_t v) {
	uint8_t d[6] = {0x84, 0x03, vcp, v >> 8, v & 0xff, 0};
	d[5] = 0x6E ^ 0x51 ^ d[0] ^ d[1] ^ d[2] ^ d[3] ^ d[4];
	IOReturn r = -1;
	for (int i = 0; i < 2; i++) { usleep(10000); r = aosI2C(svc, 1, d, 6); }
	return r;
}

// aosDDCGet sends "Get VCP Feature" and reads the 11-byte reply into out.
static int aosDDCGet(CFTypeRef svc, uint8_t vcp, uint8_t *out, uint32_t n) {
	uint8_t d[4] = {0x82, 0x01, vcp, 0};
	d[3] = 0x6E ^ 0x51 ^ d[0] ^ d[1] ^ d[2];
	IOReturn r = -1;
	for (int i = 0; i < 2; i++) { usleep(10000); r = aosI2C(svc, 1, d, 4); }
	if (r != 0) return r;
	usleep(50000);
	return aosI2C(svc, 0, out, n);
}
*/
import "C"

import (
	"fmt"
	"math"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine"
)

const vcpBrightness = 0x10

func brightnessScreens(ms []engine.Monitor) ([]screen, error) {
	out := make([]screen, 0, len(ms))
	for _, m := range ms {
		s := screen{ID: m.ID, Name: monitorName(m)}
		d, err := cgDisplay(m)
		if err != nil {
			s.Reason = err.Error()
			out = append(out, s)
			continue
		}
		if C.aosIsBuiltin(d) != 0 {
			builtinScreen(&s, d)
		} else {
			ddcScreen(&s, d)
		}
		out = append(out, s)
	}
	return out, nil
}

// monitorName is the name macOS shows for the monitor in System Settings.
func monitorName(m engine.Monitor) string {
	d, err := cgDisplay(m)
	if err != nil {
		return fmt.Sprintf("Display %d", m.ID)
	}
	var buf [128]C.char
	if C.aosScreenName(d, &buf[0], C.int(len(buf))) == 0 {
		return C.GoString(&buf[0])
	}
	if C.aosIsBuiltin(d) != 0 {
		return "Built-in Display"
	}
	return fmt.Sprintf("Display %d", m.ID)
}

func builtinScreen(s *screen, d C.CGDirectDisplayID) {
	s.Method = "builtin"
	if C.aosIsAsleep(d) != 0 {
		s.Reason = "the built-in panel is asleep (lid closed or display off)"
		return
	}
	var v C.float
	if C.aosGetBrightness(d, &v) != 0 {
		s.Reason = "macOS does not expose brightness for this panel"
		return
	}
	s.get = func() (int, error) {
		if C.aosIsAsleep(d) != 0 {
			return 0, fmt.Errorf("the built-in panel is asleep")
		}
		var v C.float
		if C.aosGetBrightness(d, &v) != 0 {
			return 0, fmt.Errorf("could not read the panel's brightness")
		}
		return int(math.Round(float64(v) * 100)), nil
	}
	s.set = func(pct int) error {
		if C.aosSetBrightness(d, C.float(float64(pct)/100)) != 0 {
			return fmt.Errorf("macOS refused the brightness change")
		}
		return nil
	}
}

func ddcScreen(s *screen, d C.CGDirectDisplayID) {
	s.Method = "ddc"
	if C.aosIsAsleep(d) != 0 {
		s.Reason = "the monitor is asleep"
		return
	}
	svc := C.aosDDCService(d)
	if svc == 0 {
		s.Reason = "no DDC/CI route to this monitor (needs Apple Silicon; some docks and HDMI ports block it)"
		return
	}
	var max int
	read := func() (int, error) {
		var lastErr error
		for try := 0; try < 4; try++ {
			if try > 0 {
				time.Sleep(40 * time.Millisecond)
			}
			var buf [12]byte
			if rc := C.aosDDCGet(svc, vcpBrightness, (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.uint32_t(len(buf))); rc != 0 {
				lastErr = fmt.Errorf("DDC/CI read failed (IOKit error 0x%x)", uint32(rc))
				continue
			}
			cur, m, err := ddcReply(buf[:], vcpBrightness)
			if err != nil {
				lastErr = err
				continue
			}
			max = m
			return toPercent(cur, m), nil
		}
		return 0, lastErr
	}
	s.get = read
	s.set = func(pct int) error {
		if max == 0 {
			if _, err := read(); err != nil {
				return err
			}
		}
		if rc := C.aosDDCSet(svc, vcpBrightness, C.uint16_t(fromPercent(pct, max))); rc != 0 {
			return fmt.Errorf("DDC/CI write failed (IOKit error 0x%x)", uint32(rc))
		}
		time.Sleep(100 * time.Millisecond)
		return nil
	}
}
