package groups

/*
#cgo LDFLAGS: -framework CoreAudio -framework CoreFoundation
#include <CoreAudio/CoreAudio.h>
#include <CoreFoundation/CoreFoundation.h>
#include <stdlib.h>

static AudioObjectPropertyAddress aosAddr(AudioObjectPropertySelector s, AudioObjectPropertyScope sc) {
	AudioObjectPropertyAddress a = { s, sc, kAudioObjectPropertyElementMain };
	return a;
}

// aosDeviceIDs fills ids (up to max) and returns the count, or -1.
static int aosDeviceIDs(AudioDeviceID *ids, int max) {
	AudioObjectPropertyAddress a = aosAddr(kAudioHardwarePropertyDevices, kAudioObjectPropertyScopeGlobal);
	UInt32 size = max * sizeof(AudioDeviceID);
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &size, ids) != noErr) return -1;
	return size / sizeof(AudioDeviceID);
}

// aosHasStreams reports whether the device has streams in the scope.
static int aosHasStreams(AudioDeviceID id, int input) {
	AudioObjectPropertyAddress a = aosAddr(kAudioDevicePropertyStreams,
		input ? kAudioObjectPropertyScopeInput : kAudioObjectPropertyScopeOutput);
	UInt32 size = 0;
	if (AudioObjectGetPropertyDataSize(id, &a, 0, NULL, &size) != noErr) return 0;
	return size > 0;
}

// aosString copies a CFString property into buf as UTF-8.
static int aosString(AudioDeviceID id, AudioObjectPropertySelector sel, char *buf, int len) {
	AudioObjectPropertyAddress a = aosAddr(sel, kAudioObjectPropertyScopeGlobal);
	CFStringRef s = NULL;
	UInt32 size = sizeof(s);
	if (AudioObjectGetPropertyData(id, &a, 0, NULL, &size, &s) != noErr || s == NULL) return 0;
	Boolean ok = CFStringGetCString(s, buf, len, kCFStringEncodingUTF8);
	CFRelease(s);
	return ok;
}

static AudioDeviceID aosDefault(int input) {
	AudioObjectPropertyAddress a = aosAddr(input ? kAudioHardwarePropertyDefaultInputDevice : kAudioHardwarePropertyDefaultOutputDevice, kAudioObjectPropertyScopeGlobal);
	AudioDeviceID id = 0;
	UInt32 size = sizeof(id);
	AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &size, &id);
	return id;
}

static OSStatus aosSetDefault(AudioDeviceID id, int input) {
	AudioObjectPropertyAddress a = aosAddr(input ? kAudioHardwarePropertyDefaultInputDevice : kAudioHardwarePropertyDefaultOutputDevice, kAudioObjectPropertyScopeGlobal);
	OSStatus st = AudioObjectSetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, sizeof(id), &id);
	if (st == noErr && !input) {
		// Alerts and UI sounds follow the output too, as System Settings does.
		a.mSelector = kAudioHardwarePropertyDefaultSystemOutputDevice;
		AudioObjectSetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, sizeof(id), &id);
	}
	return st;
}
*/
import "C"

import (
	"fmt"
	"strconv"
	"unsafe"
)

// coreAudioDevice is one Core Audio device id with its UID.
type coreAudioDevice struct {
	id  C.AudioDeviceID
	uid string
}

// coreAudioDevices lists every device; the id aos shows is the stable UID.
func coreAudioDevices(str func(C.AudioDeviceID, C.AudioObjectPropertySelector) string) ([]coreAudioDevice, error) {
	ids := make([]C.AudioDeviceID, 256)
	n := int(C.aosDeviceIDs(&ids[0], C.int(len(ids))))
	if n < 0 {
		return nil, fmt.Errorf("Core Audio would not list devices")
	}
	out := make([]coreAudioDevice, n)
	for i, id := range ids[:n] {
		uid := str(id, C.kAudioDevicePropertyDeviceUID)
		if uid == "" {
			uid = strconv.Itoa(int(id))
		}
		out[i] = coreAudioDevice{id: id, uid: uid}
	}
	return out, nil
}

func withStringReader(fn func(str func(C.AudioDeviceID, C.AudioObjectPropertySelector) string) error) error {
	buf := (*C.char)(C.malloc(1024))
	defer C.free(unsafe.Pointer(buf))
	return fn(func(id C.AudioDeviceID, sel C.AudioObjectPropertySelector) string {
		if C.aosString(id, sel, buf, 1024) == 0 {
			return ""
		}
		return C.GoString(buf)
	})
}

func listAudioDevices() ([]audioDevice, error) {
	var out []audioDevice
	err := withStringReader(func(str func(C.AudioDeviceID, C.AudioObjectPropertySelector) string) error {
		devs, err := coreAudioDevices(str)
		if err != nil {
			return err
		}
		for _, dir := range []string{"output", "input"} {
			input, def := C.int(0), C.aosDefault(0)
			if dir == "input" {
				input, def = 1, C.aosDefault(1)
			}
			for _, d := range devs {
				if C.aosHasStreams(d.id, input) == 0 {
					continue
				}
				out = append(out, audioDevice{ID: d.uid, Name: str(d.id, C.kAudioObjectPropertyName), Direction: dir, Default: d.id == def})
			}
		}
		return nil
	})
	return out, err
}

func setDefaultAudioDevice(d audioDevice) error {
	return withStringReader(func(str func(C.AudioDeviceID, C.AudioObjectPropertySelector) string) error {
		devs, err := coreAudioDevices(str)
		if err != nil {
			return err
		}
		input := C.int(0)
		if d.Direction == "input" {
			input = 1
		}
		for _, cd := range devs {
			if cd.uid == d.ID {
				if st := C.aosSetDefault(cd.id, input); st != 0 {
					return fmt.Errorf("Core Audio refused the switch (OSStatus %d)", int(st))
				}
				return nil
			}
		}
		return fmt.Errorf("device %q went away", d.Name)
	})
}
