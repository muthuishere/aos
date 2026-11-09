package groups

/*
#cgo LDFLAGS: -framework CoreAudio
#include <CoreAudio/CoreAudio.h>
#include <libproc.h>
#include <stdlib.h>

// Core Audio process objects (macOS 14.2+). The selectors are written as
// their four-char codes so an older SDK still builds; an older OS answers
// the query with an error, which reads as unsupported.
#define AOS_PROCESS_LIST   0x70727323 // 'prs#' kAudioHardwarePropertyProcessObjectList
#define AOS_PROCESS_PID    0x70706964 // 'ppid' kAudioProcessPropertyPID
#define AOS_PROCESS_OUTPUT 0x7069726F // 'piro' kAudioProcessPropertyIsRunningOutput

static int aosProcessObjects(AudioObjectID *ids, int max) {
	AudioObjectPropertyAddress a = { AOS_PROCESS_LIST, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain };
	UInt32 size = max * sizeof(AudioObjectID);
	if (AudioObjectGetPropertyData(kAudioObjectSystemObject, &a, 0, NULL, &size, ids) != noErr) return -1;
	return size / sizeof(AudioObjectID);
}

static UInt32 aosProcessU32(AudioObjectID id, AudioObjectPropertySelector sel) {
	AudioObjectPropertyAddress a = { sel, kAudioObjectPropertyScopeGlobal, kAudioObjectPropertyElementMain };
	UInt32 v = 0, size = sizeof(v);
	if (AudioObjectGetPropertyData(id, &a, 0, NULL, &size, &v) != noErr) return 0;
	return v;
}

static int aosPidPath(int pid, char *buf, int len) {
	return proc_pidpath(pid, buf, len);
}
*/
import "C"

import (
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
)

// listAudioApps lists the processes Core Audio sees playing output now.
// macOS keeps no per-app volume, so Volume and Muted stay null.
func listAudioApps() ([]audioApp, error) {
	ids := make([]C.AudioObjectID, 1024)
	n := int(C.aosProcessObjects(&ids[0], C.int(len(ids))))
	if n < 0 {
		return nil, &cli.ExitError{Code: 2, Message: "unsupported: listing apps playing audio needs macOS 14.2 or later (Core Audio process objects)"}
	}
	buf := (*C.char)(C.malloc(4096))
	defer C.free(unsafe.Pointer(buf))
	var out []audioApp
	for _, id := range ids[:n] {
		if C.aosProcessU32(id, C.AOS_PROCESS_OUTPUT) == 0 {
			continue
		}
		pid := int(C.aosProcessU32(id, C.AOS_PROCESS_PID))
		path := ""
		if C.aosPidPath(C.int(pid), buf, 4096) > 0 {
			path = C.GoString(buf)
		}
		out = append(out, audioApp{ID: strconv.Itoa(int(id)), Name: macAppName(path, pid), PID: pid, Playing: true})
	}
	return out, nil
}

// macAppName names a process by its outermost .app bundle, so a browser's
// "Google Chrome Helper.app" inside Google Chrome.app reads as Google Chrome.
func macAppName(path string, pid int) string {
	if path == "" {
		return "pid " + strconv.Itoa(pid)
	}
	if i := strings.Index(path, ".app/"); i >= 0 {
		return filepath.Base(path[:i])
	}
	return filepath.Base(path)
}

// setAudioApps refuses: macOS has no per-app volume. A process tap can mute
// an app only while the process holding the tap stays alive, and changing
// the level means re-playing the app's audio through an aggregate device,
// which needs a resident helper (or a virtual driver). aos is a one-shot CLI
// and installs neither, so it says so rather than pretend.
func setAudioApps([]audioApp, int, int) error {
	return &cli.ExitError{Code: 2, Message: "unsupported: macOS has no per-app volume or mute; Core Audio process taps only hold while a helper keeps running, and aos installs no resident helper or virtual audio driver"}
}
