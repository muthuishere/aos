package groups

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Device listing through IMMDeviceEnumerator, and switching through
// IPolicyConfig: undocumented, but the interface the Sound control panel
// itself uses, stable since Windows 7. All COM, no helper processes.

var (
	procAudioCoTaskMemFree    = audioOle32.NewProc("CoTaskMemFree")
	procAudioPropVariantClear = audioOle32.NewProc("PropVariantClear")

	clsidPolicyConfigClient = windows.GUID{Data1: 0x870AF99C, Data2: 0x171D, Data3: 0x4F9E, Data4: [8]byte{0xAF, 0x0D, 0xE6, 0x3D, 0xF4, 0x0C, 0x2B, 0xC9}}
	iidIPolicyConfig        = windows.GUID{Data1: 0xF8679F50, Data2: 0x850A, Data3: 0x41CF, Data4: [8]byte{0x9C, 0x72, 0x43, 0x0F, 0x29, 0x02, 0x90, 0xC8}}
	// PKEY_Device_FriendlyName
	pkeyFriendlyName = struct {
		fmtid windows.GUID
		pid   uint32
	}{windows.GUID{Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD, Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0}}, 14}
)

const (
	// IMMDeviceEnumerator 3 EnumAudioEndpoints; IMMDeviceCollection 3
	// GetCount, 4 Item; IMMDevice 4 OpenPropertyStore, 5 GetId;
	// IPropertyStore 5 GetValue; IPolicyConfig 13 SetDefaultEndpoint.
	vtEnumAudioEndpoints = 3
	vtCollectionCount    = 3
	vtCollectionItem     = 4
	vtOpenPropertyStore  = 4
	vtGetID              = 5
	vtPropGetValue       = 5
	vtSetDefaultEndpoint = 13

	eCapture          = 1
	deviceStateActive = 1
	stgmRead          = 0
	vtLPWSTR          = 31
)

// withAudioCOM runs fn on one locked thread with COM initialised and a
// device enumerator, as withEndpointVolume does.
func withAudioCOM(fn func(enum uintptr) error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- func() error {
			hr, _, _ := procAudioCoInitializeEx.Call(0, coinitApartment)
			if hr == 0 || hr == 1 {
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
			return fn(enum)
		}()
	}()
	return <-done
}

// deviceID reads IMMDevice.GetId, "" on failure.
func deviceID(dev uintptr) string {
	var p uintptr
	if int32(audioCall(dev, vtGetID, uintptr(unsafe.Pointer(&p)))) < 0 || p == 0 {
		return ""
	}
	defer procAudioCoTaskMemFree.Call(p)
	return windows.UTF16PtrToString((*uint16)(osPtr(p)))
}

// friendlyName reads PKEY_Device_FriendlyName from the property store.
func friendlyName(dev uintptr) string {
	var store uintptr
	if int32(audioCall(dev, vtOpenPropertyStore, stgmRead, uintptr(unsafe.Pointer(&store)))) < 0 {
		return ""
	}
	defer audioCall(store, vtRelease)
	var pv [24]byte // PROPVARIANT
	if int32(audioCall(store, vtPropGetValue, uintptr(unsafe.Pointer(&pkeyFriendlyName)), uintptr(unsafe.Pointer(&pv[0])))) < 0 {
		return ""
	}
	defer procAudioPropVariantClear.Call(uintptr(unsafe.Pointer(&pv[0])))
	if *(*uint16)(unsafe.Pointer(&pv[0])) != vtLPWSTR {
		return ""
	}
	p := *(*uintptr)(unsafe.Pointer(&pv[8]))
	if p == 0 {
		return ""
	}
	return windows.UTF16PtrToString((*uint16)(osPtr(p)))
}

func listAudioDevices() ([]audioDevice, error) {
	var out []audioDevice
	err := withAudioCOM(func(enum uintptr) error {
		for _, flow := range []struct {
			flow uintptr
			dir  string
		}{{eRender, "output"}, {eCapture, "input"}} {
			def := ""
			var d uintptr
			if int32(audioCall(enum, vtGetDefaultAudioEndpoint, flow.flow, eConsole, uintptr(unsafe.Pointer(&d)))) >= 0 {
				def = deviceID(d)
				audioCall(d, vtRelease)
			}
			var coll uintptr
			if err := audioHR("EnumAudioEndpoints", audioCall(enum, vtEnumAudioEndpoints, flow.flow, deviceStateActive, uintptr(unsafe.Pointer(&coll)))); err != nil {
				return err
			}
			var n uint32
			audioCall(coll, vtCollectionCount, uintptr(unsafe.Pointer(&n)))
			for i := uint32(0); i < n; i++ {
				var dev uintptr
				if int32(audioCall(coll, vtCollectionItem, uintptr(i), uintptr(unsafe.Pointer(&dev)))) < 0 {
					continue
				}
				id := deviceID(dev)
				out = append(out, audioDevice{ID: id, Name: friendlyName(dev), Direction: flow.dir, Default: id != "" && id == def})
				audioCall(dev, vtRelease)
			}
			audioCall(coll, vtRelease)
		}
		return nil
	})
	return out, err
}

// setDefaultAudioDevice makes the device the default for every role
// (console, multimedia, communications), as the Sound panel does.
func setDefaultAudioDevice(d audioDevice) error {
	return withAudioCOM(func(uintptr) error {
		var pc uintptr
		hr, _, _ := procAudioCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidPolicyConfigClient)), 0, clsctxAll,
			uintptr(unsafe.Pointer(&iidIPolicyConfig)), uintptr(unsafe.Pointer(&pc)))
		if err := audioHR("CoCreateInstance(PolicyConfig)", hr); err != nil {
			return err
		}
		defer audioCall(pc, vtRelease)
		id, err := windows.UTF16PtrFromString(d.ID)
		if err != nil {
			return err
		}
		for role := uintptr(0); role < 3; role++ {
			if err := audioHR("SetDefaultEndpoint", audioCall(pc, vtSetDefaultEndpoint, uintptr(unsafe.Pointer(id)), role)); err != nil {
				return err
			}
		}
		return nil
	})
}
