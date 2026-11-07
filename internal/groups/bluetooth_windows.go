package groups

import (
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows"
)

// The classic Bluetooth API in bthprops.cpl: radio presence and the paired
// device list. Switching the radio and connecting live in WinRT
// (Windows.Devices.Radios), which aos does not wrap, so those exit 2.

var (
	bthprops                     = windows.NewLazySystemDLL("bthprops.cpl")
	procBluetoothFindFirstRadio  = bthprops.NewProc("BluetoothFindFirstRadio")
	procBluetoothFindRadioClose  = bthprops.NewProc("BluetoothFindRadioClose")
	procBluetoothFindFirstDevice = bthprops.NewProc("BluetoothFindFirstDevice")
	procBluetoothFindNextDevice  = bthprops.NewProc("BluetoothFindNextDevice")
	procBluetoothFindDeviceClose = bthprops.NewProc("BluetoothFindDeviceClose")
)

// btFindRadioParams is BLUETOOTH_FIND_RADIO_PARAMS.
type btFindRadioParams struct{ Size uint32 }

// btDeviceSearchParams is BLUETOOTH_DEVICE_SEARCH_PARAMS.
type btDeviceSearchParams struct {
	Size                uint32
	ReturnAuthenticated int32
	ReturnRemembered    int32
	ReturnUnknown       int32
	ReturnConnected     int32
	IssueInquiry        int32
	TimeoutMultiplier   uint8
	Radio               windows.Handle
}

// btDeviceInfo is BLUETOOTH_DEVICE_INFO.
type btDeviceInfo struct {
	Size          uint32
	Address       uint64
	ClassOfDevice uint32
	Connected     int32
	Remembered    int32
	Authenticated int32
	LastSeen      windows.Systemtime
	LastUsed      windows.Systemtime
	Name          [248]uint16
}

func readBluetooth() (btStatus, error) {
	p := btFindRadioParams{Size: uint32(unsafe.Sizeof(btFindRadioParams{}))}
	var radio windows.Handle
	find, _, _ := procBluetoothFindFirstRadio.Call(uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&radio)))
	if find == 0 {
		// No radio, or the radio is switched off: Windows hides both alike.
		return btStatus{}, nil
	}
	defer procBluetoothFindRadioClose.Call(find)
	defer windows.CloseHandle(radio)
	return btStatus{Present: true, On: true}, nil
}

func listBluetooth() ([]btDevice, error) {
	params := btDeviceSearchParams{ReturnAuthenticated: 1, ReturnRemembered: 1, ReturnConnected: 1}
	params.Size = uint32(unsafe.Sizeof(params))
	info := btDeviceInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	find, _, _ := procBluetoothFindFirstDevice.Call(uintptr(unsafe.Pointer(&params)), uintptr(unsafe.Pointer(&info)))
	if find == 0 {
		return nil, nil
	}
	defer procBluetoothFindDeviceClose.Call(find)
	var devs []btDevice
	for {
		devs = append(devs, btDevice{
			Name:      windows.UTF16ToString(info.Name[:]),
			Address:   formatBTAddress(info.Address),
			Connected: info.Connected != 0,
		})
		info = btDeviceInfo{}
		info.Size = uint32(unsafe.Sizeof(info))
		if r, _, _ := procBluetoothFindNextDevice.Call(find, uintptr(unsafe.Pointer(&info))); r == 0 {
			return devs, nil
		}
	}
}

func setBluetoothPower(bool) error {
	return &cli.ExitError{Code: 2, Message: "switching Bluetooth on Windows needs the WinRT Radios API, which aos does not use yet; use Quick Settings"}
}

func connectBluetooth(string, bool) error {
	return &cli.ExitError{Code: 2, Message: "connecting a Bluetooth device on Windows needs WinRT, which aos does not use yet; use Settings > Bluetooth"}
}
