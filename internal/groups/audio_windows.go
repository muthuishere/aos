package groups

import (
	"fmt"
	"math"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Master volume and mute through Core Audio COM, called directly from Go:
// MMDeviceEnumerator -> default render endpoint -> IAudioEndpointVolume.
// Windows ships no command-line volume control, and this needs no helper.

var (
	audioOle32                = windows.NewLazySystemDLL("ole32.dll")
	procAudioCoInitializeEx   = audioOle32.NewProc("CoInitializeEx")
	procAudioCoUninitialize   = audioOle32.NewProc("CoUninitialize")
	procAudioCoCreateInstance = audioOle32.NewProc("CoCreateInstance")

	clsidMMDeviceEnumerator = windows.GUID{Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C, Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = windows.GUID{Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35, Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioEndpointVolume = windows.GUID{Data1: 0x5CDF2C82, Data2: 0x841E, Data3: 0x4546, Data4: [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A}}
)

// Vtable slots. IUnknown is 0 QueryInterface, 1 AddRef, 2 Release; the
// interface's own methods follow in declaration order (mmdeviceapi.h,
// endpointvolume.h). A wrong index does not fail: it calls the neighbour.
const (
	vtRelease = 2
	// IMMDeviceEnumerator: 3 EnumAudioEndpoints, 4 GetDefaultAudioEndpoint
	vtGetDefaultAudioEndpoint = 4
	// IMMDevice: 3 Activate
	vtActivate = 3
	// IAudioEndpointVolume: 3 RegisterControlChangeNotify, 4 Unregister...,
	// 5 GetChannelCount, 6 SetMasterVolumeLevel, 7 SetMasterVolumeLevelScalar,
	// 8 GetMasterVolumeLevel, 9 GetMasterVolumeLevelScalar, 10-13 channel
	// levels, 14 SetMute, 15 GetMute, 16 GetVolumeStepInfo, 17 VolumeStepUp,
	// 18 VolumeStepDown.
	vtSetMasterVolumeLevelScalar = 7
	vtGetMasterVolumeLevelScalar = 9
	vtSetMute                    = 14
	vtGetMute                    = 15
	vtGetVolumeStepInfo          = 16
	vtVolumeStepUp               = 17
	vtVolumeStepDown             = 18

	eRender         = 0
	eConsole        = 0
	clsctxAll       = 0x17
	coinitApartment = 0x2
)

// audioCall calls vtable slot i of a COM object and returns the HRESULT.
func audioCall(obj uintptr, i int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(osPtr(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

// osPtr reinterprets an address the OS handed back (a COM object, a global
// memory block) as a pointer. That memory is not Go's, so the GC never moves
// it, which is the case go vet cannot see.
func osPtr(u uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&u)) }

func audioHR(what string, hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s failed: HRESULT 0x%08X", what, uint32(hr))
	}
	return nil
}

// withEndpointVolume runs fn with the default speakers' IAudioEndpointVolume,
// on one locked OS thread with COM initialised, releasing everything after.
func withEndpointVolume(fn func(ep uintptr) error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- func() error {
			hr, _, _ := procAudioCoInitializeEx.Call(0, coinitApartment)
			if hr == 0 || hr == 1 { // S_OK or S_FALSE: ours to undo
				defer procAudioCoUninitialize.Call()
			} else if err := audioHR("CoInitializeEx", hr); err != nil {
				return err
			}
			var enum uintptr
			hr, _, _ = procAudioCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
				uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enum)))
			if err := audioHR("CoCreateInstance(MMDeviceEnumerator)", hr); err != nil {
				return err
			}
			defer audioCall(enum, vtRelease)
			var dev uintptr
			if err := audioHR("GetDefaultAudioEndpoint", audioCall(enum, vtGetDefaultAudioEndpoint, eRender, eConsole, uintptr(unsafe.Pointer(&dev)))); err != nil {
				return fmt.Errorf("%w (no speakers or headphones?)", err)
			}
			defer audioCall(dev, vtRelease)
			var ep uintptr
			if err := audioHR("IMMDevice.Activate", audioCall(dev, vtActivate, uintptr(unsafe.Pointer(&iidIAudioEndpointVolume)), clsctxAll, 0, uintptr(unsafe.Pointer(&ep)))); err != nil {
				return err
			}
			defer audioCall(ep, vtRelease)
			return fn(ep)
		}()
	}()
	return <-done
}

func readScalar(ep uintptr) (float32, error) {
	var v float32
	err := audioHR("GetMasterVolumeLevelScalar", audioCall(ep, vtGetMasterVolumeLevelScalar, uintptr(unsafe.Pointer(&v))))
	return v, err
}

func getVolume() (int, error) {
	var level int
	err := withEndpointVolume(func(ep uintptr) error {
		v, err := readScalar(ep)
		level = int(math.Round(float64(v) * 100))
		return err
	})
	return level, err
}

func setVolume(level int) error {
	target := float32(level) / 100
	return withEndpointVolume(func(ep uintptr) error {
		if runtime.GOARCH != "arm64" {
			// amd64: the syscall trampoline copies each argument into XMM0-3
			// too, so a float in slot 1 lands where the callee reads it.
			// 386: stdcall passes everything on the stack.
			return audioHR("SetMasterVolumeLevelScalar",
				audioCall(ep, vtSetMasterVolumeLevelScalar, uintptr(math.Float32bits(target)), 0))
		}
		// arm64: Go's Windows trampoline never loads the float registers
		// (internal/runtime/syscall/windows/asm_windows_arm64.s), so a float
		// argument cannot be passed. Walk the endpoint's own volume steps
		// instead; those calls take no floats.
		return stepVolume(ep, target)
	})
}

// stepVolume moves to the step nearest target with VolumeStepUp/Down.
func stepVolume(ep uintptr, target float32) error {
	var step, count uint32
	if err := audioHR("GetVolumeStepInfo", audioCall(ep, vtGetVolumeStepInfo, uintptr(unsafe.Pointer(&step)), uintptr(unsafe.Pointer(&count)))); err != nil {
		return err
	}
	if count < 2 {
		return fmt.Errorf("this audio device reports no volume steps")
	}
	cur, err := readScalar(ep)
	if err != nil {
		return err
	}
	unit := 1 / float64(count-1)
	moves := int(math.Round((float64(target) - float64(cur)) / unit))
	slot := vtVolumeStepUp
	if moves < 0 {
		slot, moves = vtVolumeStepDown, -moves
	}
	for i := 0; i < moves; i++ {
		if err := audioHR("VolumeStep", audioCall(ep, slot, 0)); err != nil {
			return err
		}
	}
	return nil
}

func getMute() (bool, error) {
	var muted int32 // BOOL
	err := withEndpointVolume(func(ep uintptr) error {
		return audioHR("GetMute", audioCall(ep, vtGetMute, uintptr(unsafe.Pointer(&muted))))
	})
	return muted != 0, err
}

func setMute(muted bool) error {
	var b uintptr
	if muted {
		b = 1
	}
	return withEndpointVolume(func(ep uintptr) error {
		return audioHR("SetMute", audioCall(ep, vtSetMute, b, 0))
	})
}
