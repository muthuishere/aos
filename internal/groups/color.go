package groups

import (
	"fmt"
	"image"
	_ "image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// colorInfo is one sampled screen pixel, in `color at --json` shape.
type colorInfo struct {
	X   int    `json:"x"`
	Y   int    `json:"y"`
	Hex string `json:"hex"`
	R   uint8  `json:"r"`
	G   uint8  `json:"g"`
	B   uint8  `json:"b"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("color", "Read the color of a pixel on screen")
		r.Add(
			&cli.Command{
				Group: "color", Name: "at",
				Summary: "Print the color of the screen pixel at x,y (global screen points)",
				Args:    "<x> <y> [--json]",
				Examples: []string{
					"aos color at 640 400         # #1E1E1E  rgb(30, 30, 30)",
					"aos color at -1200 300 --json",
				},
				Run: runColorAt,
			},
			&cli.Command{
				Group: "color", Name: "pick",
				Summary: "Print the color under the mouse pointer, after an optional delay to point",
				Args:    "[--delay=<seconds>] [--json]",
				Examples: []string{
					"aos color pick",
					"aos color pick --delay=3     # 3 s to move the pointer onto the color",
				},
				Run: runColorPick,
			},
		)
	})
}

func runColorAt(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	x, y, err := parseXY(set.Rest)
	if err != nil {
		return err
	}
	return printColor(c, x, y, set.Has("json"))
}

func runColorPick(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "delay")
	if err != nil {
		return err
	}
	if err := set.Reject("delay", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`color pick` takes no positional arguments; for a fixed point use `color at <x> <y>`")
	}
	delay, err := strconv.ParseFloat(set.String("delay", "0"), 64)
	if err != nil || delay < 0 || delay > 60 {
		return fmt.Errorf("--delay wants seconds between 0 and 60")
	}
	time.Sleep(time.Duration(delay * float64(time.Second)))
	x, y, err := engine.CursorPosition()
	if err != nil {
		return err
	}
	return printColor(c, x, y, set.Has("json"))
}

func parseXY(rest []string) (int, int, error) {
	if len(rest) == 1 && strings.Contains(rest[0], ",") {
		rest = strings.SplitN(rest[0], ",", 2)
	}
	if len(rest) != 2 {
		return 0, 0, fmt.Errorf("`color at` needs x and y, e.g. aos color at 640 400")
	}
	x, err1 := strconv.Atoi(strings.TrimSpace(rest[0]))
	y, err2 := strconv.Atoi(strings.TrimSpace(rest[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("x and y must be whole numbers, got %q %q", rest[0], rest[1])
	}
	return x, y, nil
}

func printColor(c *cli.Ctx, x, y int, asJSON bool) error {
	img, err := grabPixel(x, y)
	if err != nil {
		return err
	}
	info := pixelColor(img, x, y)
	if asJSON {
		return writeJSON(c, info)
	}
	c.Printf("%s  rgb(%d, %d, %d)\n", info.Hex, info.R, info.G, info.B)
	return nil
}

// grabPixel captures the 1x1 point at x,y through the same engine path as
// `capture screenshot --region`, so it works on every OS and monitor layout.
func grabPixel(x, y int) (image.Image, error) {
	dir, err := os.MkdirTemp("", "aos-color-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "pixel.png")
	if _, err := engine.Screenshot(engine.ScreenshotOptions{Region: &engine.Rect{X: x, Y: y, W: 1, H: 1}, OutPath: out}); err != nil {
		return nil, err
	}
	f, err := os.Open(out)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// pixelColor reads the top-left pixel of the capture (a Retina point is 2x2
// pixels of the same color, so any of them would do).
func pixelColor(img image.Image, x, y int) colorInfo {
	b := img.Bounds()
	r, g, bl, _ := img.At(b.Min.X, b.Min.Y).RGBA()
	info := colorInfo{X: x, Y: y, R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(bl >> 8)}
	info.Hex = fmt.Sprintf("#%02X%02X%02X", info.R, info.G, info.B)
	return info
}
