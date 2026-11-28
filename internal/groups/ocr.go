package groups

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/ocr"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("ocr", "Read the text in an image file with the OS's own OCR")
		r.Add(&cli.Command{
			Group:   "ocr",
			Summary: "Read the text in a PNG, JPEG or GIF file (or - for stdin): lines in reading order with boxes in image pixels",
			Args:    "<image|-> [--lang=en-US,de-DE] [--fast] [--json]",
			Examples: []string{
				"aos ocr receipt.png",
				"aos ocr shot.png --json",
				"aos capture screenshot --out=- | aos ocr -",
			},
			Run: runOCR,
		})
	})
}

func runOCR(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "lang")
	if err != nil {
		return err
	}
	if err := set.Reject("lang", "fast", "json"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`aos ocr` needs one image: a file, or - for stdin")
	}
	opts := ocr.Options{Fast: set.Has("fast")}
	for _, l := range strings.Split(set.String("lang", ""), ",") {
		if l = strings.TrimSpace(l); l != "" {
			opts.Languages = append(opts.Languages, l)
		}
	}
	var lines []ocr.Line
	if src := set.Rest[0]; src == "-" {
		img, _, derr := image.Decode(c.Stdin)
		if derr != nil {
			return fmt.Errorf("ocr: stdin is not a PNG, JPEG or GIF: %w", derr)
		}
		lines, err = ocr.RecognizeImage(img, opts)
	} else {
		if _, serr := os.Stat(expandHome(src)); serr != nil {
			return serr
		}
		lines, err = ocr.RecognizeFile(expandHome(src), opts)
	}
	if err != nil {
		return err
	}
	if set.Has("json") {
		if lines == nil {
			lines = []ocr.Line{}
		}
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(lines)
	}
	for _, l := range lines {
		c.Println(strings.TrimRight(l.Text, " "))
	}
	return nil
}
