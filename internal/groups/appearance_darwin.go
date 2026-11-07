package groups

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <dlfcn.h>

// Night Shift has no public API. CBBlueLightClient in the private
// CoreBrightness framework is what Control Center uses; it is loaded with
// dlopen, so a macOS without it reports "unavailable" instead of crashing.
// Its status struct begins {BOOL active; BOOL enabled; ...}; the buffer is
// oversized so a longer struct in a newer macOS cannot overrun it.
@interface CBBlueLightClient : NSObject
- (BOOL)setEnabled:(BOOL)enabled;
- (BOOL)getBlueLightStatus:(void *)status;
@end

static id aosBlueLight(void) {
	static id client;
	if (client) return client;
	if (!dlopen("/System/Library/PrivateFrameworks/CoreBrightness.framework/CoreBrightness", RTLD_LAZY)) return nil;
	Class cls = NSClassFromString(@"CBBlueLightClient");
	if (!cls) return nil;
	client = [[cls alloc] init];
	return client;
}

// 1 on, 0 off, -1 unavailable.
static int aosNightShiftGet(void) {
	id c = aosBlueLight();
	if (!c) return -1;
	unsigned char status[256] = {0};
	if (![c getBlueLightStatus:status]) return -1;
	return status[0] ? 1 : 0;
}

static int aosNightShiftSet(int on) {
	id c = aosBlueLight();
	if (!c) return -1;
	return [c setEnabled:(on ? YES : NO)] ? 0 : -1;
}
*/
import "C"

import (
	"fmt"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

func getDarkMode() (bool, error) {
	out, err := sys.Osascript(`tell application "System Events" to tell appearance preferences to get dark mode`)
	if err != nil {
		return false, err
	}
	return out == "true", nil
}

func setDarkMode(on bool) error {
	_, err := sys.Osascript(fmt.Sprintf(`tell application "System Events" to tell appearance preferences to set dark mode to %t`, on))
	return err
}

var errNoNightShift = &cli.ExitError{Code: 2, Message: "Night Shift is not available on this Mac"}

func getNightLight() (bool, error) {
	switch C.aosNightShiftGet() {
	case 1:
		return true, nil
	case 0:
		return false, nil
	}
	return false, errNoNightShift
}

func setNightLight(on bool) error {
	v := C.int(0)
	if on {
		v = 1
	}
	if C.aosNightShiftSet(v) != 0 {
		return errNoNightShift
	}
	return nil
}
