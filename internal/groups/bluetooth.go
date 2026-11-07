package groups

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// btStatus is what `bluetooth status --json` emits.
type btStatus struct {
	Present bool   `json:"present"`
	On      bool   `json:"on"`
	Address string `json:"address,omitempty"`
}

// btDevice is one paired (or currently connected) device.
type btDevice struct {
	Name      string `json:"name"`
	Address   string `json:"address"`
	Connected bool   `json:"connected"`
	Type      string `json:"type,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("bluetooth", "Bluetooth: radio state, paired devices, connect")
		r.Add(
			&cli.Command{
				Group: "bluetooth", Name: "status",
				Summary: "Print whether Bluetooth is present and on",
				Args:    "[--json]",
				Examples: []string{
					"aos bluetooth status",
					"aos bluetooth status --json",
				},
				Run: runBluetoothStatus,
			},
			&cli.Command{
				Group: "bluetooth", Name: "list",
				Summary: "List paired devices and whether each is connected",
				Args:    "[--json]",
				Examples: []string{
					"aos bluetooth list",
					"aos bluetooth list --json",
				},
				Run: runBluetoothList,
			},
			&cli.Command{
				Group: "bluetooth", Name: "on",
				Summary:  "Turn the Bluetooth radio on (Linux)",
				Examples: []string{"aos bluetooth on"},
				Run:      func(c *cli.Ctx, args []string) error { return runBluetoothPower(c, args, true) },
			},
			&cli.Command{
				Group: "bluetooth", Name: "off",
				Summary:  "Turn the Bluetooth radio off (Linux; drops keyboards and mice, confirm first)",
				Examples: []string{"aos bluetooth off"},
				Run:      func(c *cli.Ctx, args []string) error { return runBluetoothPower(c, args, false) },
			},
			&cli.Command{
				Group: "bluetooth", Name: "connect",
				Summary: "Connect a paired device by name or address",
				Args:    "<name|address>",
				Examples: []string{
					"aos bluetooth connect AirPods",
					"aos bluetooth connect D1:08:D1:0E:60:DB",
				},
				Run: func(c *cli.Ctx, args []string) error { return runBluetoothConnect(c, args, true) },
			},
			&cli.Command{
				Group: "bluetooth", Name: "disconnect",
				Summary:  "Disconnect a paired device by name or address",
				Args:     "<name|address>",
				Examples: []string{"aos bluetooth disconnect AirPods"},
				Run:      func(c *cli.Ctx, args []string) error { return runBluetoothConnect(c, args, false) },
			},
		)
	})
}

func runBluetoothStatus(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("bluetooth status", args)
	if err != nil {
		return err
	}
	st, err := readBluetooth()
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(c, st)
	}
	switch {
	case !st.Present:
		c.Println("no bluetooth")
	case st.On:
		c.Println("on")
	default:
		c.Println("off")
	}
	return nil
}

func runBluetoothList(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("bluetooth list", args)
	if err != nil {
		return err
	}
	devs, err := listBluetooth()
	if err != nil {
		return err
	}
	sortDevices(devs)
	if asJSON {
		if devs == nil {
			devs = []btDevice{}
		}
		return writeJSON(c, devs)
	}
	for _, d := range devs {
		state := "         "
		if d.Connected {
			state = "connected"
		}
		c.Printf("%s  %s  %s\n", d.Address, state, d.Name)
	}
	return nil
}

func runBluetoothPower(c *cli.Ctx, args []string, on bool) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	if err := setBluetoothPower(on); err != nil {
		return err
	}
	c.Printf("bluetooth %s\n", onOff(on))
	return nil
}

func runBluetoothConnect(c *cli.Ctx, args []string, connect bool) error {
	verb := map[bool]string{true: "connect", false: "disconnect"}[connect]
	if len(args) == 0 {
		return fmt.Errorf("`bluetooth %s` needs a device name or address; see `aos bluetooth list`", verb)
	}
	devs, err := listBluetooth()
	if err != nil {
		return err
	}
	dev, err := resolveBluetooth(devs, strings.Join(args, " "))
	if err != nil {
		return err
	}
	if err := connectBluetooth(dev.Address, connect); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	c.Printf("%sed %s  %s\n", verb, dev.Address, dev.Name)
	return nil
}

func sortDevices(devs []btDevice) {
	sort.SliceStable(devs, func(i, j int) bool {
		if devs[i].Connected != devs[j].Connected {
			return devs[i].Connected
		}
		return strings.ToLower(devs[i].Name) < strings.ToLower(devs[j].Name)
	})
}

var btAddress = regexp.MustCompile(`^(?i)[0-9a-f]{2}([:-][0-9a-f]{2}){5}$`)

// resolveBluetooth picks one paired device by address, exact name, or a
// unique name substring; anything ambiguous is refused with the candidates.
func resolveBluetooth(devs []btDevice, query string) (btDevice, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	if btAddress.MatchString(q) {
		want := strings.ReplaceAll(q, "-", ":")
		for _, d := range devs {
			if strings.ToLower(strings.ReplaceAll(d.Address, "-", ":")) == want {
				return d, nil
			}
		}
		return btDevice{}, &cli.ExitError{Code: 1, Message: fmt.Sprintf("no paired device with address %s", query)}
	}
	var hits []btDevice
	for _, d := range devs {
		name := strings.ToLower(d.Name)
		if name == q {
			return d, nil
		}
		if strings.Contains(name, q) {
			hits = append(hits, d)
		}
	}
	switch len(hits) {
	case 0:
		return btDevice{}, &cli.ExitError{Code: 1, Message: fmt.Sprintf("no paired device matching %q; see `aos bluetooth list`", query)}
	case 1:
		return hits[0], nil
	}
	var names []string
	for _, d := range hits {
		names = append(names, fmt.Sprintf("  %s  %s", d.Address, d.Name))
	}
	return btDevice{}, &cli.ExitError{Code: 1, Message: fmt.Sprintf("%q matches %d devices; use the address:\n%s", query, len(hits), strings.Join(names, "\n"))}
}

// parseSystemProfilerBT reads `system_profiler SPBluetoothDataType -json`.
func parseSystemProfilerBT(data []byte) (btStatus, []btDevice, error) {
	var doc struct {
		Items []struct {
			Controller   map[string]string                       `json:"controller_properties"`
			Connected    []map[string]map[string]json.RawMessage `json:"device_connected"`
			NotConnected []map[string]map[string]json.RawMessage `json:"device_not_connected"`
		} `json:"SPBluetoothDataType"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return btStatus{}, nil, err
	}
	var st btStatus
	var devs []btDevice
	str := func(m map[string]json.RawMessage, k string) string {
		var s string
		_ = json.Unmarshal(m[k], &s)
		return s
	}
	add := func(list []map[string]map[string]json.RawMessage, connected bool) {
		for _, entry := range list {
			for name, props := range entry {
				devs = append(devs, btDevice{Name: name, Address: str(props, "device_address"), Connected: connected, Type: str(props, "device_minorType")})
			}
		}
	}
	for _, it := range doc.Items {
		if it.Controller != nil {
			st.Present = true
			st.On = it.Controller["controller_state"] == "attrib_on"
			st.Address = it.Controller["controller_address"]
		}
		add(it.Connected, true)
		add(it.NotConnected, false)
	}
	return st, devs, nil
}

// parseBluetoothctlDevices reads `bluetoothctl devices [Paired|Connected]`:
// "Device AA:BB:CC:DD:EE:FF Some Name".
func parseBluetoothctlDevices(out string) []btDevice {
	var devs []btDevice
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] != "Device" || !btAddress.MatchString(f[1]) {
			continue
		}
		devs = append(devs, btDevice{Address: f[1], Name: strings.Join(f[2:], " ")})
	}
	return devs
}

// parseBluetoothctlShow reads `bluetoothctl show`.
func parseBluetoothctlShow(out string) btStatus {
	var st btStatus
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "Controller" {
			st.Present, st.Address = true, f[1]
		}
		if v, ok := strings.CutPrefix(line, "Powered:"); ok {
			st.On = strings.TrimSpace(v) == "yes"
		}
	}
	return st
}

// formatBTAddress renders a Windows 48-bit address as AA:BB:CC:DD:EE:FF.
func formatBTAddress(a uint64) string {
	b := make([]string, 6)
	for i := 0; i < 6; i++ {
		b[i] = fmt.Sprintf("%02X", byte(a>>(8*(5-i))))
	}
	return strings.Join(b, ":")
}
