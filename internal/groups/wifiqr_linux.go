package groups

import (
	"strings"

	"github.com/muthuishere/aos/internal/sys"
)

// wifiPassword reads the saved PSK of the NetworkManager connection named
// after the network.
func wifiPassword(ssid string) (string, error) {
	out, err := sys.Output("nmcli", "-s", "-g", "802-11-wireless-security.psk", "connection", "show", ssid)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
