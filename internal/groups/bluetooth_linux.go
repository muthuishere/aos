package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

func needBluetoothctl() error {
	if !sys.Has("bluetoothctl") {
		return &cli.ExitError{Code: 2, Message: "Bluetooth needs bluetoothctl (BlueZ), which this machine does not have"}
	}
	return nil
}

func readBluetooth() (btStatus, error) {
	if err := needBluetoothctl(); err != nil {
		return btStatus{}, err
	}
	out, err := sys.Output("bluetoothctl", "show")
	if err != nil {
		// No controller: bluetoothctl says so and fails.
		return btStatus{}, nil
	}
	return parseBluetoothctlShow(out), nil
}

func listBluetooth() ([]btDevice, error) {
	if err := needBluetoothctl(); err != nil {
		return nil, err
	}
	out, err := sys.Output("bluetoothctl", "devices", "Paired")
	if err != nil {
		return nil, err
	}
	devs := parseBluetoothctlDevices(out)
	// `devices Connected` needs BlueZ 5.65+; older versions just show nothing.
	if conn, err := sys.Output("bluetoothctl", "devices", "Connected"); err == nil {
		live := map[string]bool{}
		for _, d := range parseBluetoothctlDevices(conn) {
			live[d.Address] = true
		}
		for i := range devs {
			devs[i].Connected = live[devs[i].Address]
		}
	}
	return devs, nil
}

func setBluetoothPower(on bool) error {
	if err := needBluetoothctl(); err != nil {
		return err
	}
	_, err := sys.Output("bluetoothctl", "power", onOff(on))
	return err
}

func connectBluetooth(address string, connect bool) error {
	verb := "disconnect"
	if connect {
		verb = "connect"
	}
	_, err := sys.Output("bluetoothctl", verb, address)
	return err
}
