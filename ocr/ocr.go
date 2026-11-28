// Package ocr reads the text in an image with the operating system's own
// on-device OCR engine: Apple Vision on macOS, Windows.Media.Ocr on Windows.
// No cgo, no external engine, no network: it builds with CGO_ENABLED=0 and
// cross-compiles. Inside WSL a Linux build runs the Windows OCR through
// aos.exe (Options.NoWSLHop turns that off); elsewhere on Linux, and on other
// systems, every call returns ErrUnsupported.
//
// Coordinates are image pixels, origin at the top left. Lines come back in
// reading order: top to bottom, and left to right within a row.
package ocr

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // decoders for RecognizeFile
	_ "image/jpeg" // decoders for RecognizeFile
	_ "image/png"  // decoders for RecognizeFile
	"os"
	"sort"
)

// ErrUnsupported is returned where the OS has no OCR engine aos can reach
// (Linux, and any OS other than macOS and Windows).
var ErrUnsupported = errors.New("ocr: no OS text recognition on this platform (macOS and Windows only)")

// Options tune recognition. The zero value is the accurate setting aos uses
// itself: Vision's accurate level with language correction on macOS, the
// user-profile languages on Windows.
type Options struct {
	// Languages are BCP 47 tags in order of preference ("en-US", "de-DE").
	// macOS only; Windows uses the languages of the user profile.
	Languages []string
	// Fast trades accuracy for speed (Vision's fast level). macOS only.
	Fast bool
	// NoWSLHop keeps a Linux build inside WSL from starting the Windows
	// aos.exe to do the recognition; it returns ErrUnsupported instead.
	NoWSLHop bool
}

// Word is one word of a Line, boxed in image pixels.
type Word struct {
	Text string  `json:"text"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
}

// Line is one line of recognized text. Confidence is 0..1 on macOS; Windows
// does not report one, so it is 0 there.
type Line struct {
	Text       string  `json:"text"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	W          float64 `json:"w"`
	H          float64 `json:"h"`
	Confidence float64 `json:"confidence"`
	Words      []Word  `json:"words"`
}

// RecognizeFile decodes a PNG, JPEG or GIF file and reads its text. Inside
// WSL a Linux build hands the file to the Windows aos.exe (aos ocr), unless
// opts.NoWSLHop is set.
func RecognizeFile(path string, opts Options) ([]Line, error) {
	if lines, err, handled := wslFile(path, opts); handled {
		return lines, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("ocr: %s is not a PNG, JPEG or GIF: %w", path, err)
	}
	return RecognizeImage(img, opts)
}

// RecognizeImage reads the text in img.
func RecognizeImage(img image.Image, opts Options) ([]Line, error) {
	if img == nil || img.Bounds().Dx() <= 0 || img.Bounds().Dy() <= 0 {
		return nil, errors.New("ocr: empty image")
	}
	lines, err := recognize(img, opts)
	if err != nil {
		return nil, err
	}
	readingOrder(lines)
	return lines, nil
}

// readingOrder sorts lines top to bottom; lines whose vertical centres sit
// within half a line height of each other form a row, read left to right.
func readingOrder(lines []Line) {
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Y < lines[j].Y })
	for start := 0; start < len(lines); {
		end := start + 1
		cy := lines[start].Y + lines[start].H/2
		for end < len(lines) {
			h := min(lines[start].H, lines[end].H)
			if d := lines[end].Y + lines[end].H/2 - cy; d > h/2 || d < -h/2 {
				break
			}
			end++
		}
		row := lines[start:end]
		sort.SliceStable(row, func(i, j int) bool { return row[i].X < row[j].X })
		start = end
	}
}
