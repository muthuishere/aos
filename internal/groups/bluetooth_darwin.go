package groups

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework IOBluetooth -framework Foundation
#include <stdlib.h>
#import <IOBluetooth/IOBluetooth.h>

// aosBTConnect opens or closes the baseband connection to a paired device
// through the public IOBluetooth API. Returns an IOReturn, -1 when unknown.
static int aosBTConnect(const char *addr, int connect) {
	@autoreleasepool {
		IOBluetoothDevice *d = [IOBluetoothDevice deviceWithAddressString:[NSString stringWithUTF8String:addr]];
		if (d == nil) return -1;
		return connect ? [d openConnection] : [d closeConnection];
	}
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

func readBluetoothProfile() (btStatus, []btDevice, error) {
	out, err := sys.Output("system_profiler", "SPBluetoothDataType", "-json")
	if err != nil {
		return btStatus{}, nil, err
	}
	return parseSystemProfilerBT([]byte(out))
}

func readBluetooth() (btStatus, error) {
	st, _, err := readBluetoothProfile()
	return st, err
}

func listBluetooth() ([]btDevice, error) {
	_, devs, err := readBluetoothProfile()
	return devs, err
}

// Switching the radio needs IOBluetooth's private power call or the
// blueutil tool; aos uses neither.
func setBluetoothPower(bool) error {
	return &cli.ExitError{Code: 2, Message: "macOS has no public API to switch Bluetooth on or off; use Control Center"}
}

func connectBluetooth(address string, connect bool) error {
	ca := C.CString(address)
	defer C.free(unsafe.Pointer(ca))
	flag := C.int(0)
	if connect {
		flag = 1
	}
	if r := C.aosBTConnect(ca, flag); r != 0 {
		return fmt.Errorf("IOBluetooth returned %d for %s", int(r), address)
	}
	return nil
}
