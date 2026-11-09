package groups

import (
	"math"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/muthuishere/aos/internal/cli"
)

// Per-app volume is the Volume Mixer's own API: the default speakers'
// IAudioSessionManager2 enumerates audio sessions, and each session's
// ISimpleAudioVolume holds its level and mute. All COM, no helper.

var (
	iidIAudioSessionManager2 = windows.GUID{Data1: 0x77AA99A0, Data2: 0x1BD6, Data3: 0x484F, Data4: [8]byte{0x8B, 0xC7, 0x2C, 0x65, 0x4C, 0x9A, 0x9B, 0x6F}}
	iidIAudioSessionControl2 = windows.GUID{Data1: 0xBFB7FF88, Data2: 0x7239, Data3: 0x4FC9, Data4: [8]byte{0x8F, 0xA2, 0x07, 0xC9, 0x50, 0xBE, 0x9C, 0x6D}}
	iidISimpleAudioVolume    = windows.GUID{Data1: 0x87CE5498, Data2: 0x68D6, Data3: 0x44E5, Data4: [8]byte{0x92, 0x15, 0x6D, 0xA4, 0x7E, 0xF8, 0x83, 0xD8}}
)

const (
	vtQueryInterface = 0
	// IAudioSessionManager2: 3 GetAudioSessionControl, 4 GetSimpleAudioVolume,
	// 5 GetSessionEnumerator. IAudioSessionEnumerator: 3 GetCount, 4 GetSession.
	vtGetSessionEnumerator = 5
	vtSessionCount         = 3
	vtSessionItem          = 4
	// IAudioSessionControl: 3 GetState ... 11; IAudioSessionControl2 adds
	// 12 GetSessionIdentifier, 13 GetSessionInstanceIdentifier,
	// 14 GetProcessId, 15 IsSystemSoundsSession.
	vtSessionGetState     = 3
	vtSessionInstanceID   = 13
	vtSessionGetProcessID = 14
	// ISimpleAudioVolume: 3 SetMasterVolume, 4 GetMasterVolume, 5 SetMute, 6 GetMute.
	vtSimpleSetVolume = 3
	vtSimpleGetVolume = 4
	vtSimpleSetMute   = 5
	vtSimpleGetMute   = 6

	sessionActive  = 1
	sessionExpired = 2
)

// eachAudioSession calls fn with every live session on the default speakers:
// its row and its ISimpleAudioVolume, both valid only during the call.
func eachAudioSession(fn func(a audioApp, vol uintptr) error) error {
	return withAudioCOM(func(enum uintptr) error {
		var dev uintptr
		if err := audioHR("GetDefaultAudioEndpoint", audioCall(enum, vtGetDefaultAudioEndpoint, eRender, eConsole, uintptr(unsafe.Pointer(&dev)))); err != nil {
			return err
		}
		defer audioCall(dev, vtRelease)
		var mgr uintptr
		if err := audioHR("Activate(IAudioSessionManager2)", audioCall(dev, vtActivate, uintptr(unsafe.Pointer(&iidIAudioSessionManager2)), clsctxAll, 0, uintptr(unsafe.Pointer(&mgr)))); err != nil {
			return err
		}
		defer audioCall(mgr, vtRelease)
		var sessions uintptr
		if err := audioHR("GetSessionEnumerator", audioCall(mgr, vtGetSessionEnumerator, uintptr(unsafe.Pointer(&sessions)))); err != nil {
			return err
		}
		defer audioCall(sessions, vtRelease)
		var n int32
		audioCall(sessions, vtSessionCount, uintptr(unsafe.Pointer(&n)))
		for i := int32(0); i < n; i++ {
			var ctl uintptr
			if int32(audioCall(sessions, vtSessionItem, uintptr(i), uintptr(unsafe.Pointer(&ctl)))) < 0 {
				continue
			}
			err := func() error {
				defer audioCall(ctl, vtRelease)
				var ctl2, vol uintptr
				if int32(audioCall(ctl, vtQueryInterface, uintptr(unsafe.Pointer(&iidIAudioSessionControl2)), uintptr(unsafe.Pointer(&ctl2)))) < 0 {
					return nil
				}
				defer audioCall(ctl2, vtRelease)
				if int32(audioCall(ctl, vtQueryInterface, uintptr(unsafe.Pointer(&iidISimpleAudioVolume)), uintptr(unsafe.Pointer(&vol)))) < 0 {
					return nil
				}
				defer audioCall(vol, vtRelease)
				var state int32
				audioCall(ctl2, vtSessionGetState, uintptr(unsafe.Pointer(&state)))
				if state == sessionExpired {
					return nil
				}
				var pid uint32
				audioCall(ctl2, vtSessionGetProcessID, uintptr(unsafe.Pointer(&pid)))
				a := audioApp{ID: sessionInstanceID(ctl2), PID: int(pid), Playing: state == sessionActive}
				a.Name = "System sounds"
				if pid != 0 {
					base := filepath.Base(processImage(pid))
					a.Name = strings.TrimSuffix(base, filepath.Ext(base))
					if a.Name == "" || a.Name == "." {
						a.Name = "pid " + strconv.Itoa(int(pid))
					}
				}
				var level float32
				var muted int32
				if int32(audioCall(vol, vtSimpleGetVolume, uintptr(unsafe.Pointer(&level)))) >= 0 {
					v := int(math.Round(float64(level) * 100))
					a.Volume = &v
				}
				if int32(audioCall(vol, vtSimpleGetMute, uintptr(unsafe.Pointer(&muted)))) >= 0 {
					m := muted != 0
					a.Muted = &m
				}
				return fn(a, vol)
			}()
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func sessionInstanceID(ctl2 uintptr) string {
	var p uintptr
	if int32(audioCall(ctl2, vtSessionInstanceID, uintptr(unsafe.Pointer(&p)))) < 0 || p == 0 {
		return ""
	}
	defer procAudioCoTaskMemFree.Call(p)
	return windows.UTF16PtrToString((*uint16)(osPtr(p)))
}

func listAudioApps() ([]audioApp, error) {
	var out []audioApp
	err := eachAudioSession(func(a audioApp, _ uintptr) error {
		out = append(out, a)
		return nil
	})
	return out, err
}

func setAudioApps(targets []audioApp, volume, mute int) error {
	if volume >= 0 && runtime.GOARCH == "arm64" {
		// Go's Windows arm64 syscall trampoline never loads float registers,
		// and SetMasterVolume takes a float with no stepping alternative.
		return &cli.ExitError{Code: 2, Message: "unsupported: setting a per-app volume on Windows arm64 (Go cannot pass a float to COM there); mute and unmute work"}
	}
	want := map[string]bool{}
	for _, t := range targets {
		want[t.ID] = true
	}
	return eachAudioSession(func(a audioApp, vol uintptr) error {
		if !want[a.ID] {
			return nil
		}
		if volume >= 0 {
			// amd64 copies each argument into XMM0-3 too, so the float in
			// slot 1 lands where the callee reads it; 386 passes on the stack.
			return audioHR("SetMasterVolume", audioCall(vol, vtSimpleSetVolume, uintptr(math.Float32bits(float32(volume)/100)), 0))
		}
		return audioHR("SetMute", audioCall(vol, vtSimpleSetMute, uintptr(mute), 0))
	})
}
