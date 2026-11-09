package groups

/*
#cgo LDFLAGS: -framework CoreMediaIO -framework CoreAudio -framework CoreFoundation
#include <CoreMediaIO/CMIOHardware.h>
#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

static void aosAppend(char *buf, int len, const char *s) {
	size_t used = strlen(buf);
	if (used + strlen(s) + 2 >= (size_t)len) return;
	if (used > 0) buf[used++] = '\n';
	strcpy(buf + used, s);
}

static void aosCFString(CFStringRef s, char *out, int len) {
	out[0] = 0;
	if (s != NULL) CFStringGetCString(s, out, len, kCFStringEncodingUTF8);
}

// aosCameraBusy appends the name of every camera some app is running, and
// returns how many cameras exist (-1 on error). kCMIODevicePropertyDeviceIsRunningSomewhere
// is the public flag the OS itself uses for the green camera light: it asks
// "is any process streaming from this device", and does not open the camera.
static int aosCameraBusy(char *buf, int len, int *busyCount) {
	*busyCount = 0;
	CMIOObjectPropertyAddress a = { kCMIOHardwarePropertyDevices, kCMIOObjectPropertyScopeGlobal, kCMIOObjectPropertyElementMain };
	UInt32 size = 0;
	if (CMIOObjectGetPropertyDataSize(kCMIOObjectSystemObject, &a, 0, NULL, &size) != 0) return -1;
	int n = size / sizeof(CMIOObjectID);
	if (n == 0) return 0;
	CMIOObjectID *ids = malloc(size);
	UInt32 used = 0;
	if (CMIOObjectGetPropertyData(kCMIOObjectSystemObject, &a, 0, NULL, size, &used, ids) != 0) { free(ids); return -1; }
	for (int i = 0; i < n; i++) {
		CMIOObjectPropertyAddress r = { kCMIODevicePropertyDeviceIsRunningSomewhere, kCMIOObjectPropertyScopeGlobal, kCMIOObjectPropertyElementMain };
		UInt32 running = 0, u = 0;
		if (CMIOObjectGetPropertyData(ids[i], &r, 0, NULL, sizeof(running), &u, &running) != 0 || !running) continue;
		(*busyCount)++;
		CMIOObjectPropertyAddress nm = { kCMIOObjectPropertyName, kCMIOObjectPropertyScopeGlobal, kCMIOObjectPropertyElementMain };
		CFStringRef s = NULL;
		UInt32 sz = sizeof(s);
		char name[256] = "";
		if (CMIOObjectGetPropertyData(ids[i], &nm, 0, NULL, sz, &u, &s) == 0 && s != NULL) {
			aosCFString(s, name, sizeof(name));
			CFRelease(s);
		}
		aosAppend(buf, len, name[0] ? name : "camera");
	}
	free(ids);
	return n;
}

static AudioObjectPropertyAddress aosAudioAddr(AudioObjectPropertySelector s) {
	AudioObjectPropertyAddress a = { s, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain };
	return a;
}

// aosMicApps appends the bundle id of every process that is recording right
// now, using the per-process audio objects (macOS 14.2 and later, public in
// AudioHardware.h). Returns the number of audio processes, or -1 when the OS
// does not have them.
static int aosMicApps(char *buf, int len, int *busyCount) {
	*busyCount = 0;
	AudioObjectPropertyAddress a = aosAudioAddr(kAudioHardwarePropertyProcessObjectList);
	UInt32 size = 0;
	if (AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &a, 0, NULL, &size) != 0) return -1;
	int n = size / sizeof(AudioObjectID);
	if (n == 0) return 0;
	AudioObjectID *ids = malloc(size);
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &size, ids) != 0) { free(ids); return -1; }
	for (int i = 0; i < n; i++) {
		AudioObjectPropertyAddress r = aosAudioAddr(kAudioProcessPropertyIsRunningInput);
		UInt32 running = 0, sz = sizeof(running);
		if (AudioObjectGetPropertyData(ids[i], &r, 0, NULL, &sz, &running) != 0 || !running) continue;
		(*busyCount)++;
		AudioObjectPropertyAddress b = aosAudioAddr(kAudioProcessPropertyBundleID);
		CFStringRef s = NULL;
		sz = sizeof(s);
		char name[256] = "";
		if (AudioObjectGetPropertyData(ids[i], &b, 0, NULL, &sz, &s) == 0 && s != NULL) {
			aosCFString(s, name, sizeof(name));
			CFRelease(s);
		}
		if (!name[0]) {
			AudioObjectPropertyAddress p = aosAudioAddr(kAudioProcessPropertyPID);
			pid_t pid = 0;
			sz = sizeof(pid);
			if (AudioObjectGetPropertyData(ids[i], &p, 0, NULL, &sz, &pid) == 0) snprintf(name, sizeof(name), "pid %d", (int)pid);
		}
		aosAppend(buf, len, name[0] ? name : "unknown app");
	}
	free(ids);
	return n;
}

// aosMicDevices is the older, device-level answer: it appends every input
// device that some process is running, without saying which. Duplex devices
// (headsets) also read busy while they only play, which is why the process
// list above is preferred.
static int aosMicDevices(char *buf, int len, int *busyCount) {
	*busyCount = 0;
	AudioObjectPropertyAddress a = aosAudioAddr(kAudioHardwarePropertyDevices);
	UInt32 size = 0;
	if (AudioObjectGetPropertyDataSize(kAudioObjectSystemObject, &a, 0, NULL, &size) != 0) return -1;
	int n = size / sizeof(AudioDeviceID);
	if (n == 0) return 0;
	AudioDeviceID *ids = malloc(size);
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &size, ids) != 0) { free(ids); return -1; }
	for (int i = 0; i < n; i++) {
		AudioObjectPropertyAddress st = { kAudioDevicePropertyStreams, kAudioObjectPropertyScopeInput, kAudioObjectPropertyElementMain };
		UInt32 ssz = 0;
		if (AudioObjectGetPropertyDataSize(ids[i], &st, 0, NULL, &ssz) != 0 || ssz == 0) continue;
		AudioObjectPropertyAddress r = aosAudioAddr(kAudioDevicePropertyDeviceIsRunningSomewhere);
		UInt32 running = 0, sz = sizeof(running);
		if (AudioObjectGetPropertyData(ids[i], &r, 0, NULL, &sz, &running) != 0 || !running) continue;
		(*busyCount)++;
		AudioObjectPropertyAddress nm = aosAudioAddr(kAudioObjectPropertyName);
		CFStringRef s = NULL;
		sz = sizeof(s);
		char name[256] = "";
		if (AudioObjectGetPropertyData(ids[i], &nm, 0, NULL, &sz, &s) == 0 && s != NULL) {
			aosCFString(s, name, sizeof(name));
			CFRelease(s);
		}
		aosAppend(buf, len, name[0] ? name : "microphone");
	}
	free(ids);
	return n;
}
*/
import "C"

import (
	"strings"
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
)

func splitNames(buf *C.char) []string {
	s := C.GoString(buf)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// readCameraUse asks CoreMediaIO whether any camera is running somewhere. The
// OS does not say which app, only which device.
func readCameraUse() (deviceUse, error) {
	buf := (*C.char)(C.calloc(1, 4096))
	defer C.free(unsafe.Pointer(buf))
	var busy C.int
	if C.aosCameraBusy(buf, 4096, &busy) < 0 {
		return deviceUse{}, &cli.ExitError{Code: 1, Message: "CoreMediaIO would not list the cameras"}
	}
	return deviceUse{Busy: busy > 0, Devices: splitNames(buf)}, nil
}

// readMicUse prefers the per-process list, which names the app and ignores a
// headset that is only playing; on a macOS without it, it falls back to the
// device flag.
func readMicUse() (deviceUse, error) {
	buf := (*C.char)(C.calloc(1, 4096))
	defer C.free(unsafe.Pointer(buf))
	var busy C.int
	if C.aosMicApps(buf, 4096, &busy) >= 0 {
		return deviceUse{Busy: busy > 0, Apps: splitNames(buf)}, nil
	}
	if C.aosMicDevices(buf, 4096, &busy) < 0 {
		return deviceUse{}, &cli.ExitError{Code: 1, Message: "CoreAudio would not list the input devices"}
	}
	return deviceUse{Busy: busy > 0, Devices: splitNames(buf)}, nil
}
