package groups

import (
	"fmt"
	"image/png"
	"os"
	"strings"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/muthuishere/aos/internal/cli"
)

// `network wifi-qr` shows the QR a phone camera scans to join the current
// Wi-Fi. The password is opt-in (--password): reading it asks the OS keychain,
// which on macOS shows an authorization prompt.

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "network", Name: "wifi-qr",
			Summary: "Show a QR code a phone can scan to join the current Wi-Fi (password only with --password)",
			Args:    "[--ssid=<name>] [--password] [--out=<file.png>] [--payload]",
			Examples: []string{
				"aos network wifi-qr                       # QR in the terminal, network name only",
				"aos network wifi-qr --password            # include the saved password (macOS asks for authorization)",
				"aos network wifi-qr --password --out=wifi.png",
				"aos network wifi-qr --ssid=GuestNet --payload   # print the WIFI:... text instead of a QR",
			},
			Run: runWifiQR,
		})
	})
}

func runWifiQR(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "ssid", "out")
	if err != nil {
		return err
	}
	if err := set.Reject("ssid", "out", "password", "payload"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`network wifi-qr` takes no positional arguments; name another network with --ssid")
	}
	ssid := set.String("ssid", "")
	if ssid == "" {
		state, err := readNetwork()
		if err != nil {
			return err
		}
		ssid = state.SSID
		// macOS 15 answers "<redacted>" to processes without Location access.
		if ssid == "<redacted>" {
			return &cli.ExitError{Code: 1, Message: "macOS hides the network name from this terminal (no Location access); name it with --ssid"}
		}
	}
	if ssid == "" {
		return &cli.ExitError{Code: 1, Message: "not connected to Wi-Fi; name the network with --ssid"}
	}

	password := ""
	if set.Has("password") {
		if password, err = wifiPassword(ssid); err != nil {
			return &cli.ExitError{Code: 1, Message: fmt.Sprintf("could not read the saved password for %q: %v", ssid, err)}
		}
	}
	payload := wifiPayload(ssid, password)

	if set.Has("payload") {
		c.Println(payload)
		return nil
	}
	if out := set.String("out", ""); out != "" {
		code, err := qrcode.NewQRCodeWriter().Encode(payload, gozxing.BarcodeFormat_QR_CODE, 512, 512, nil)
		if err != nil {
			return err
		}
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		if err := png.Encode(f, code); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		c.Println(out)
	} else {
		art, err := terminalQR(payload)
		if err != nil {
			return err
		}
		c.Printf("%s%s\n", art, ssid)
	}
	if password == "" {
		c.Warnf("no password in the code: a phone will ask for it (add --password to include it)\n")
	}
	return nil
}

// wifiPayload is the de-facto standard Wi-Fi QR text that iOS and Android
// cameras read: WIFI:T:WPA;S:<ssid>;P:<password>;;
func wifiPayload(ssid, password string) string {
	esc := strings.NewReplacer(`\`, `\\`, `;`, `\;`, `,`, `\,`, `:`, `\:`, `"`, `\"`)
	if password == "" {
		return "WIFI:T:WPA;S:" + esc.Replace(ssid) + ";;"
	}
	return "WIFI:T:WPA;S:" + esc.Replace(ssid) + ";P:" + esc.Replace(password) + ";;"
}

// terminalQR draws the code with half blocks, two modules per character
// row, dark on light with the quiet zone, so a phone can scan the terminal.
func terminalQR(payload string) (string, error) {
	// A 1x1 request makes the writer return one pixel per module plus its
	// standard 4-module quiet zone.
	m, err := qrcode.NewQRCodeWriter().Encode(payload, gozxing.BarcodeFormat_QR_CODE, 1, 1, nil)
	if err != nil {
		return "", err
	}
	w, h := m.GetWidth(), m.GetHeight()
	dark := func(x, y int) bool { return y < h && m.Get(x, y) }
	var b strings.Builder
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x++ {
			top, bottom := dark(x, y), dark(x, y+1)
			switch {
			case top && bottom:
				b.WriteString(" ")
			case top:
				b.WriteString("▄")
			case bottom:
				b.WriteString("▀")
			default:
				b.WriteString("█")
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// parseNetshKey finds "Key Content : <password>" in the output of `netsh wlan
// show profile key=clear`. The label is English-only.
func parseNetshKey(out string) (string, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "Key Content" {
			return strings.TrimSpace(v), true
		}
	}
	return "", false
}
