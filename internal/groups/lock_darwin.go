package groups

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework ApplicationServices -framework CoreFoundation
#include <ApplicationServices/ApplicationServices.h>

// aosScreenLocked: 1 locked, 0 unlocked, -1 when there is no console session.
static int aosScreenLocked(void) {
	CFDictionaryRef d = CGSessionCopyCurrentDictionary();
	if (!d) return -1;
	CFBooleanRef v = (CFBooleanRef)CFDictionaryGetValue(d, CFSTR("CGSSessionScreenIsLocked"));
	int r = (v && CFBooleanGetValue(v)) ? 1 : 0;
	CFRelease(d);
	return r;
}
*/
import "C"

import "fmt"

// screenLocked reads the CoreGraphics session dictionary: the lock flag is
// only present while the screen is locked.
func screenLocked() (bool, error) {
	switch C.aosScreenLocked() {
	case 1:
		return true, nil
	case 0:
		return false, nil
	}
	return false, fmt.Errorf("no login session to read the lock state from")
}
