package groups

import (
	"fmt"

	"github.com/muthuishere/aos/internal/sys"
)

// wifiPassword reads the saved key of the WLAN profile named after the
// network from `netsh wlan show profile key=clear`.
func wifiPassword(ssid string) (string, error) {
	out, err := sys.Output("netsh", "wlan", "show", "profile", "name="+ssid, "key=clear")
	if err != nil {
		return "", err
	}
	key, ok := parseNetshKey(out)
	if !ok {
		return "", fmt.Errorf("no key in the profile (an open network, or a non-English Windows)")
	}
	return key, nil
}
