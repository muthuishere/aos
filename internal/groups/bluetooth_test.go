package groups

import "testing"

const spBluetooth = `{"SPBluetoothDataType":[{
 "controller_properties":{"controller_address":"C0:C7:DB:1D:28:AE","controller_state":"attrib_on"},
 "device_connected":[{"AirPods Pro":{"device_address":"AA:BB:CC:DD:EE:01","device_minorType":"Headphones"}}],
 "device_not_connected":[{"Flow100@Lofree":{"device_address":"D1:08:D1:0E:60:DB","device_minorType":"Keyboard"}}]
}]}`

func TestParseSystemProfilerBT(t *testing.T) {
	st, devs, err := parseSystemProfilerBT([]byte(spBluetooth))
	if err != nil || !st.Present || !st.On || st.Address != "C0:C7:DB:1D:28:AE" {
		t.Fatalf("status = %+v %v", st, err)
	}
	if len(devs) != 2 || !devs[0].Connected || devs[0].Name != "AirPods Pro" || devs[1].Type != "Keyboard" || devs[1].Connected {
		t.Errorf("devices = %+v", devs)
	}
	st, _, _ = parseSystemProfilerBT([]byte(`{"SPBluetoothDataType":[{"controller_properties":{"controller_state":"attrib_off"}}]}`))
	if !st.Present || st.On {
		t.Errorf("off controller = %+v", st)
	}
}

func TestParseBluetoothctl(t *testing.T) {
	devs := parseBluetoothctlDevices("Device AA:BB:CC:DD:EE:01 AirPods Pro\nDevice D1:08:D1:0E:60:DB Flow100\n[CHG] noise\n")
	if len(devs) != 2 || devs[0].Name != "AirPods Pro" || devs[1].Address != "D1:08:D1:0E:60:DB" {
		t.Errorf("devices = %+v", devs)
	}
	st := parseBluetoothctlShow("Controller C0:C7:DB:1D:28:AE (public)\n\tName: box\n\tPowered: yes\n")
	if !st.Present || !st.On || st.Address != "C0:C7:DB:1D:28:AE" {
		t.Errorf("show = %+v", st)
	}
}

func TestResolveBluetooth(t *testing.T) {
	devs := []btDevice{
		{Name: "AirPods Pro", Address: "AA:BB:CC:DD:EE:01"},
		{Name: "AirPods Max", Address: "AA:BB:CC:DD:EE:02"},
		{Name: "Keyboard", Address: "D1:08:D1:0E:60:DB"},
	}
	if d, err := resolveBluetooth(devs, "keyb"); err != nil || d.Name != "Keyboard" {
		t.Errorf("substring: %+v %v", d, err)
	}
	if d, err := resolveBluetooth(devs, "aa-bb-cc-dd-ee-02"); err != nil || d.Name != "AirPods Max" {
		t.Errorf("address: %+v %v", d, err)
	}
	if d, err := resolveBluetooth(devs, "airpods pro"); err != nil || d.Address != "AA:BB:CC:DD:EE:01" {
		t.Errorf("exact: %+v %v", d, err)
	}
	if _, err := resolveBluetooth(devs, "AirPods"); err == nil {
		t.Error("ambiguous name should be refused")
	}
	if _, err := resolveBluetooth(devs, "mouse"); err == nil {
		t.Error("unknown name should fail")
	}
}

func TestFormatBTAddressAndSort(t *testing.T) {
	if got := formatBTAddress(0xD108D10E60DB); got != "D1:08:D1:0E:60:DB" {
		t.Errorf("got %q", got)
	}
	devs := []btDevice{{Name: "b"}, {Name: "a"}, {Name: "z", Connected: true}}
	sortDevices(devs)
	if devs[0].Name != "z" || devs[1].Name != "a" {
		t.Errorf("sort = %+v", devs)
	}
}
