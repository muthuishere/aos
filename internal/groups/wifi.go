package groups

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// Wi-Fi radio and joining. A password is only ever read from stdin
// (--password-stdin) and handed to the OS without touching argv, where any
// user on the machine could read it from the process list.

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "network", Name: "wifi on",
				Summary:  "Turn the Wi-Fi radio on",
				Examples: []string{"aos network wifi on"},
				Run:      func(c *cli.Ctx, args []string) error { return runWifiPower(c, args, true) },
			},
			&cli.Command{
				Group: "network", Name: "wifi off",
				Summary:  "Turn the Wi-Fi radio off (drops the connection; confirm first if you are remote)",
				Examples: []string{"aos network wifi off"},
				Run:      func(c *cli.Ctx, args []string) error { return runWifiPower(c, args, false) },
			},
			&cli.Command{
				Group: "network", Name: "wifi join",
				Summary: "Join a Wi-Fi network; a password is read from stdin, never argv",
				Args:    "<ssid> [--password-stdin]",
				Examples: []string{
					"aos network wifi join HomeNet              # saved or open network",
					"printf '%s' \"$PW\" | aos network wifi join HomeNet --password-stdin",
				},
				Run: runWifiJoin,
			},
		)
	})
}

func runWifiPower(c *cli.Ctx, args []string, on bool) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	if err := setWifiPower(on); err != nil {
		return err
	}
	c.Printf("wifi %s\n", onOff(on))
	return nil
}

func runWifiJoin(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("password-stdin"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`network wifi join` needs exactly one SSID (quote it if it has spaces)")
	}
	ssid := set.Rest[0]
	password := ""
	if set.Has("password-stdin") {
		if password, err = readSecretLine(c.Stdin); err != nil {
			return err
		}
		if password == "" {
			return fmt.Errorf("--password-stdin given but stdin was empty")
		}
	}
	if err := joinWifi(ssid, password); err != nil {
		return err
	}
	c.Printf("joined %s\n", ssid)
	return nil
}

// readSecretLine reads the first line of r, without its line ending.
func readSecretLine(r io.Reader) (string, error) {
	if r == nil {
		return "", fmt.Errorf("no stdin to read the password from")
	}
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// parseWifiDevice finds the Wi-Fi device in `networksetup -listallhardwareports`.
func parseWifiDevice(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		port, ok := strings.CutPrefix(strings.TrimSpace(line), "Hardware Port:")
		if !ok {
			continue
		}
		if port = strings.TrimSpace(port); port != "Wi-Fi" && port != "AirPort" {
			continue
		}
		for _, next := range lines[i+1:] {
			if dev, ok := strings.CutPrefix(strings.TrimSpace(next), "Device:"); ok {
				return strings.TrimSpace(dev)
			}
		}
	}
	return ""
}

// nmcliPasswdFile is the secrets file nmcli reads from stdin for `connection up`.
func nmcliPasswdFile(password string) string {
	return "802-11-wireless-security.psk:" + password + "\n"
}

// wlanProfileXML is a Windows WLAN profile for a WPA2-Personal network.
func wlanProfileXML(ssid, password string) string {
	e := xmlEscape
	return `<?xml version="1.0"?>
<WLANProfile xmlns="http://www.microsoft.com/networking/WLAN/profile/v1">
<name>` + e(ssid) + `</name>
<SSIDConfig><SSID><name>` + e(ssid) + `</name></SSID></SSIDConfig>
<connectionType>ESS</connectionType>
<connectionMode>auto</connectionMode>
<MSM><security>
<authEncryption><authentication>WPA2PSK</authentication><encryption>AES</encryption><useOneX>false</useOneX></authEncryption>
<sharedKey><keyType>passPhrase</keyType><protected>false</protected><keyMaterial>` + e(password) + `</keyMaterial></sharedKey>
</security></MSM>
</WLANProfile>`
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}
