package groups

import (
	"fmt"
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows"
)

// The native WLAN API: no netsh, so a password never lands on a command line.

var (
	wlanapi             = windows.NewLazySystemDLL("wlanapi.dll")
	procWlanOpenHandle  = wlanapi.NewProc("WlanOpenHandle")
	procWlanCloseHandle = wlanapi.NewProc("WlanCloseHandle")
	procWlanEnumIfaces  = wlanapi.NewProc("WlanEnumInterfaces")
	procWlanFreeMemory  = wlanapi.NewProc("WlanFreeMemory")
	procWlanSetIface    = wlanapi.NewProc("WlanSetInterface")
	procWlanSetProfile  = wlanapi.NewProc("WlanSetProfile")
	procWlanConnect     = wlanapi.NewProc("WlanConnect")
)

const (
	wlanIntfOpcodeRadioState   = 4
	dot11RadioOn               = 1
	dot11RadioOff              = 2
	wlanConnectionModeProfile  = 0
	dot11BssTypeInfrastructure = 1
)

// wlanPhyRadioState is WLAN_PHY_RADIO_STATE.
type wlanPhyRadioState struct {
	PhyIndex      uint32
	SoftwareState uint32
	HardwareState uint32
}

// wlanInterfaceInfoList is the head of WLAN_INTERFACE_INFO_LIST: two DWORDs,
// then WLAN_INTERFACE_INFO items, each starting with the interface GUID.
type wlanInterfaceInfoList struct {
	Count uint32
	Index uint32
	First windows.GUID
}

// wlanConnectionParameters is WLAN_CONNECTION_PARAMETERS.
type wlanConnectionParameters struct {
	Mode      uint32
	Profile   *uint16
	SSID      uintptr
	BssidList uintptr
	BssType   uint32
	Flags     uint32
}

// wlanFirstInterface opens the WLAN service and returns the first adapter's GUID.
func wlanFirstInterface() (uintptr, windows.GUID, error) {
	var negotiated uint32
	var h uintptr
	if r, _, _ := procWlanOpenHandle.Call(2, 0, uintptr(unsafe.Pointer(&negotiated)), uintptr(unsafe.Pointer(&h))); r != 0 {
		return 0, windows.GUID{}, &cli.ExitError{Code: 2, Message: fmt.Sprintf("the WLAN service is not available: %v", windows.Errno(r))}
	}
	var list *wlanInterfaceInfoList
	if r, _, _ := procWlanEnumIfaces.Call(h, 0, uintptr(unsafe.Pointer(&list))); r != 0 {
		procWlanCloseHandle.Call(h, 0)
		return 0, windows.GUID{}, windows.Errno(r)
	}
	defer procWlanFreeMemory.Call(uintptr(unsafe.Pointer(list)))
	if list.Count == 0 {
		procWlanCloseHandle.Call(h, 0)
		return 0, windows.GUID{}, &cli.ExitError{Code: 2, Message: "this machine has no Wi-Fi adapter"}
	}
	return h, list.First, nil
}

func setWifiPower(on bool) error {
	h, guid, err := wlanFirstInterface()
	if err != nil {
		return err
	}
	defer procWlanCloseHandle.Call(h, 0)
	state := wlanPhyRadioState{SoftwareState: dot11RadioOff}
	if on {
		state.SoftwareState = dot11RadioOn
	}
	if r, _, _ := procWlanSetIface.Call(h, uintptr(unsafe.Pointer(&guid)), wlanIntfOpcodeRadioState,
		unsafe.Sizeof(state), uintptr(unsafe.Pointer(&state)), 0); r != 0 {
		return windows.Errno(r)
	}
	return nil
}

// joinWifi connects with the saved profile named after the SSID; with a
// password it first writes a WPA2-Personal profile for it.
func joinWifi(ssid, password string) error {
	h, guid, err := wlanFirstInterface()
	if err != nil {
		return err
	}
	defer procWlanCloseHandle.Call(h, 0)
	if password != "" {
		xml, _ := windows.UTF16PtrFromString(wlanProfileXML(ssid, password))
		var reason uint32
		if r, _, _ := procWlanSetProfile.Call(h, uintptr(unsafe.Pointer(&guid)), 0, uintptr(unsafe.Pointer(xml)),
			0, 1, 0, uintptr(unsafe.Pointer(&reason))); r != 0 {
			return &cli.ExitError{Code: 1, Message: fmt.Sprintf("could not save the Wi-Fi profile: %v (reason %d)", windows.Errno(r), reason)}
		}
	}
	name, _ := windows.UTF16PtrFromString(ssid)
	params := wlanConnectionParameters{Mode: wlanConnectionModeProfile, Profile: name, BssType: dot11BssTypeInfrastructure}
	if r, _, _ := procWlanConnect.Call(h, uintptr(unsafe.Pointer(&guid)), uintptr(unsafe.Pointer(&params)), 0); r != 0 {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("could not join %s (no saved profile? pass --password-stdin): %v", ssid, windows.Errno(r))}
	}
	return nil
}
