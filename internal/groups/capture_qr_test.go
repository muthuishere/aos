package groups

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
)

func TestDecodeQRFile(t *testing.T) {
	code, err := qrcode.NewQRCodeWriter().Encode("WIFI:T:WPA;S:aos;P:secret;;", gozxing.BarcodeFormat_QR_CODE, 240, 240, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "qr.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, code); err != nil {
		t.Fatal(err)
	}
	f.Close()

	found, err := decodeQRFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Text != "WIFI:T:WPA;S:aos;P:secret;;" || found[0].Bounds.W <= 0 {
		t.Fatalf("got %+v", found)
	}
}

func TestDecodeQRNone(t *testing.T) {
	blank := image.NewGray(image.Rect(0, 0, 100, 100))
	for i := range blank.Pix {
		blank.Pix[i] = 0xff
	}
	found, err := decodeQR(blank)
	if err != nil || len(found) != 0 {
		t.Fatalf("blank image: %v %v", found, err)
	}
}
