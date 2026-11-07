package groups

import (
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

func wifiDevice() (string, error) {
	out, err := sys.Output("networksetup", "-listallhardwareports")
	if err != nil {
		return "", err
	}
	if dev := parseWifiDevice(out); dev != "" {
		return dev, nil
	}
	return "", &cli.ExitError{Code: 2, Message: "this Mac has no Wi-Fi interface"}
}

func setWifiPower(on bool) error {
	dev, err := wifiDevice()
	if err != nil {
		return err
	}
	_, err = sys.Output("networksetup", "-setairportpower", dev, onOff(on))
	return err
}

// joinWifi joins a saved or open network through networksetup, which takes
// the password from the keychain. networksetup only accepts a new password
// on argv, so joining a new secured network is refused rather than leaking it.
func joinWifi(ssid, password string) error {
	if password != "" {
		return &cli.ExitError{Code: 2, Message: "macOS only takes a new Wi-Fi password on the command line, where other users could read it; join it once in System Settings, then `aos network wifi join` works without a password"}
	}
	dev, err := wifiDevice()
	if err != nil {
		return err
	}
	out, err := sys.Output("networksetup", "-setairportnetwork", dev, ssid)
	if err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	// networksetup exits 0 even when the join fails, and says why on stdout.
	if out = strings.TrimSpace(out); out != "" {
		return &cli.ExitError{Code: 1, Message: out}
	}
	return nil
}
