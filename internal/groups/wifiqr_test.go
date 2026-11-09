package groups

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func TestWifiPayload(t *testing.T) {
	if got := wifiPayload("Home;Net", `p:a\ss`); got != `WIFI:T:WPA;S:Home\;Net;P:p\:a\\ss;;` {
		t.Fatalf("got %s", got)
	}
	if got := wifiPayload("Cafe", ""); got != "WIFI:T:WPA;S:Cafe;;" {
		t.Fatalf("no password: got %s", got)
	}
}

func TestParseNetshKey(t *testing.T) {
	out := "Security settings\r\n-----------------\r\n    Authentication         : WPA2-Personal\r\n    Key Content            : hunter2: x\r\n"
	if key, ok := parseNetshKey(out); !ok || key != "hunter2: x" {
		t.Fatalf("got %q %v", key, ok)
	}
	if _, ok := parseNetshKey("    Security key           : Absent\r\n"); ok {
		t.Fatal("open network has no key")
	}
}

func TestTerminalQR(t *testing.T) {
	art, err := terminalQR("WIFI:T:WPA;S:aos;;")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(art, "\n"), "\n")
	// Version 1 is 21 modules plus a 4-module quiet zone each side: 29 wide,
	// drawn two module rows per text line.
	if len(lines) < 15 || len([]rune(lines[0])) < 29 || strings.Trim(lines[0], "█") != "" {
		t.Fatalf("unexpected art:\n%s", art)
	}

	// Paint the characters back into pixels and decode them: the art must be
	// a scannable code, not just the right size.
	const scale = 6
	w := len([]rune(lines[0]))
	img := image.NewGray(image.Rect(0, 0, w*scale, len(lines)*2*scale))
	for row, line := range lines {
		for col, r := range []rune(line) {
			topDark, bottomDark := r == ' ' || r == '▄', r == ' ' || r == '▀'
			for dy := 0; dy < 2*scale; dy++ {
				dark := topDark
				if dy >= scale {
					dark = bottomDark
				}
				for dx := 0; dx < scale; dx++ {
					v := uint8(0xff)
					if dark {
						v = 0
					}
					img.SetGray(col*scale+dx, row*2*scale+dy, color.Gray{Y: v})
				}
			}
		}
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, nil)
	if err != nil || res.GetText() != "WIFI:T:WPA;S:aos;;" {
		t.Fatalf("terminal QR does not decode: %v %v", res, err)
	}
}
