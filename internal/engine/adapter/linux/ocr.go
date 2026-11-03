//go:build linux

package linux

// RecognizeText on Linux: tesseract over a capture of the rect. tesseract
// is not bundled; without it this returns ErrNotImplemented with the
// install hint, and `aos window text` / `find` stay accessibility-only.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/muthuishere/aos/internal/engine/core"
)

func (a *Adapter) RecognizeText(b core.Rect) ([]core.TextItem, error) {
	if _, err := exec.LookPath("tesseract"); err != nil {
		return nil, fmt.Errorf("%w: OCR needs tesseract (apt install tesseract-ocr / dnf install tesseract)", core.ErrNotImplemented)
	}
	dir, err := os.MkdirTemp("", "aos-ocr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	img := filepath.Join(dir, "cap.png")
	if err := a.CaptureRect(b, img); err != nil {
		return nil, err
	}
	out, err := exec.Command("tesseract", img, "stdout", "--psm", "3", "tsv").Output()
	if err != nil {
		return nil, fmt.Errorf("tesseract: %w", err)
	}
	return parseTesseractTSV(string(out), b.X, b.Y), nil
}

// parseTesseractTSV groups tesseract's word rows into lines (one per
// block/paragraph/line number), each with its words, offset to global
// coordinates.
func parseTesseractTSV(tsv string, ox, oy int) []core.TextItem {
	var lines []core.TextItem
	key := ""
	for _, row := range strings.Split(tsv, "\n") {
		f := strings.Split(row, "\t")
		if len(f) < 12 || f[0] != "5" {
			continue // level 5 = word
		}
		text := strings.TrimSpace(f[11])
		if text == "" {
			continue
		}
		var x, y, w, h int
		fmt.Sscanf(f[6]+" "+f[7]+" "+f[8]+" "+f[9], "%d %d %d %d", &x, &y, &w, &h)
		word := core.TextItem{Text: text, Role: "text", Source: "ocr", Bounds: core.Rect{X: ox + x, Y: oy + y, W: w, H: h}}
		k := f[2] + "/" + f[3] + "/" + f[4]
		if k != key || len(lines) == 0 {
			lines = append(lines, core.TextItem{Role: "text", Source: "ocr", Bounds: word.Bounds})
			key = k
		}
		l := &lines[len(lines)-1]
		if l.Text != "" {
			l.Text += " "
		}
		l.Text += text
		l.Words = append(l.Words, word)
		x0, y0 := min(l.Bounds.X, word.Bounds.X), min(l.Bounds.Y, word.Bounds.Y)
		x1 := max(l.Bounds.X+l.Bounds.W, word.Bounds.X+w)
		y1 := max(l.Bounds.Y+l.Bounds.H, word.Bounds.Y+h)
		l.Bounds = core.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
	}
	return lines
}
