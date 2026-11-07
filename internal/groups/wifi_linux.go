package groups

import (
	"os/exec"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

func needNmcli() error {
	if !sys.Has("nmcli") {
		return &cli.ExitError{Code: 2, Message: "Wi-Fi control needs NetworkManager (nmcli), which this machine does not have"}
	}
	return nil
}

func setWifiPower(on bool) error {
	if err := needNmcli(); err != nil {
		return err
	}
	_, err := sys.Output("nmcli", "radio", "wifi", onOff(on))
	return err
}

// joinWifi: without a password nmcli uses a saved or open network. With one,
// it adds a WPA-PSK connection and brings it up with the secret on stdin
// (passwd-file /dev/stdin), so it never appears in argv.
func joinWifi(ssid, password string) error {
	if err := needNmcli(); err != nil {
		return err
	}
	if password == "" {
		if _, err := sys.Output("nmcli", "device", "wifi", "connect", ssid); err != nil {
			return &cli.ExitError{Code: 1, Message: err.Error()}
		}
		return nil
	}
	if _, err := sys.Output("nmcli", "connection", "add", "type", "wifi", "con-name", ssid, "ssid", ssid,
		"wifi-sec.key-mgmt", "wpa-psk"); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	cmd := exec.Command("nmcli", "connection", "up", "id", ssid, "passwd-file", "/dev/stdin")
	cmd.Stdin = strings.NewReader(nmcliPasswdFile(password))
	if out, err := cmd.CombinedOutput(); err != nil {
		return &cli.ExitError{Code: 1, Message: strings.TrimSpace(string(out))}
	}
	return nil
}
