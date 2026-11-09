package groups

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"github.com/makiuchi-d/gozxing"
	multiqr "github.com/makiuchi-d/gozxing/multi/qrcode"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "capture", Name: "qr",
			Summary: "Decode the QR codes in an image file, a screen region or a whole monitor",
			Args:    "[<image>] [--region=<x,y,w,h>] [--monitor=<n>] [--json]",
			Examples: []string{
				"aos capture qr ticket.png                    # decode a PNG, JPEG or GIF file",
				"aos capture qr --region=100,100,400,400      # decode what is on screen there",
				"aos capture qr --monitor=2 --json            # every QR code on monitor 2, with its bounds",
			},
			Run: runCaptureQR,
		})
	})
}

// qrFound is one decoded code. Bounds are image pixels for a file, global
// screen points for a screen capture.
type qrFound struct {
	Text   string      `json:"text"`
	Bounds engine.Rect `json:"bounds"`
}

func runCaptureQR(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "region", "monitor")
	if err != nil {
		return err
	}
	if err := set.Reject("region", "monitor", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 1 {
		return fmt.Errorf("`capture qr` takes at most one image file")
	}

	path, origin := "", engine.Rect{}
	if len(set.Rest) == 1 {
		if set.Has("region") || set.Has("monitor") {
			return fmt.Errorf("give an image file or --region/--monitor, not both")
		}
		path = set.Rest[0]
	} else {
		opts := engine.ScreenshotOptions{}
		if opts.Monitor, err = set.IntPtr("monitor"); err != nil {
			return err
		}
		if region := set.String("region", ""); region != "" {
			rect, err := parseRect(region)
			if err != nil {
				return err
			}
			opts.Region = &rect
		}
		dir, err := os.MkdirTemp("", "aos-qr-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		opts.OutPath = filepath.Join(dir, "screen.png")
		if origin, err = engine.Screenshot(opts); err != nil {
			return err
		}
		path = opts.OutPath
	}

	found, err := decodeQRFile(path)
	if err != nil {
		return err
	}
	for i := range found {
		found[i].Bounds.X += origin.X
		found[i].Bounds.Y += origin.Y
	}
	if set.Has("json") {
		if found == nil {
			found = []qrFound{}
		}
		return writeJSON(c, found)
	}
	if len(found) == 0 {
		return &cli.ExitError{Code: 1, Message: "no QR code found"}
	}
	for _, q := range found {
		c.Println(q.Text)
	}
	return nil
}

func decodeQRFile(path string) ([]qrFound, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: not a PNG, JPEG or GIF image: %v", path, err)
	}
	return decodeQR(img)
}

// decodeQR finds every QR code in img. Finding none is not an error.
func decodeQR(img image.Image) ([]qrFound, error) {
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return nil, err
	}
	hints := map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_TRY_HARDER: true}
	results, err := multiqr.NewQRCodeMultiReader().DecodeMultiple(bmp, hints)
	if err != nil {
		if _, none := err.(gozxing.NotFoundException); none {
			return nil, nil
		}
		return nil, err
	}
	var found []qrFound
	for _, r := range results {
		found = append(found, qrFound{Text: r.GetText(), Bounds: pointsBounds(r.GetResultPoints())})
	}
	return found, nil
}

// pointsBounds is the box around a code's finder patterns. The finder centres
// sit inside the code, so this is a little smaller than the printed square.
func pointsBounds(pts []gozxing.ResultPoint) engine.Rect {
	if len(pts) == 0 {
		return engine.Rect{}
	}
	minX, minY, maxX, maxY := pts[0].GetX(), pts[0].GetY(), pts[0].GetX(), pts[0].GetY()
	for _, p := range pts[1:] {
		minX, maxX = min(minX, p.GetX()), max(maxX, p.GetX())
		minY, maxY = min(minY, p.GetY()), max(maxY, p.GetY())
	}
	return engine.Rect{X: int(minX), Y: int(minY), W: int(maxX - minX), H: int(maxY - minY)}
}
