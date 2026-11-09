package groups

import (
	"context"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/sys"
)

// wifiPassword asks the keychain for the saved Wi-Fi password. macOS shows an
// authorization prompt for it, so the person gets two minutes to answer.
func wifiPassword(ssid string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := sys.OutputContext(ctx, "security", "find-generic-password", "-wa", ssid)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(out, "\r\n"), nil
}
