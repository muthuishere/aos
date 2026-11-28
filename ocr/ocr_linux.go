//go:build linux

package ocr

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"

	"github.com/muthuishere/aos/internal/wslhost"
)

func execRunner(exe string, args []string) ([]byte, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = "/mnt/c" // cmd-based Windows programs dislike a UNC working directory
	if _, err := os.Stat(cmd.Dir); err != nil {
		cmd.Dir = ""
	}
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok && len(out) == 0 {
		out = ee.Stderr
	}
	return out, err
}

func inWSL(opts Options) bool {
	d := wslhost.Default()
	return !opts.NoWSLHop && wslhost.IsWSL(d.Env, d.Exists)
}

func wslFile(path string, opts Options) ([]Line, error, bool) {
	if !inWSL(opts) {
		return nil, nil, false
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err, true
	}
	lines, err := hopAOS(wslhost.Default(), execRunner, path, opts)
	if err == nil {
		readingOrder(lines)
	}
	return lines, err, true
}

func recognize(img image.Image, opts Options) ([]Line, error) {
	if !inWSL(opts) {
		return nil, ErrUnsupported
	}
	f, err := os.CreateTemp("", "aos-ocr-*.png")
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	defer os.Remove(f.Name())
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return nil, fmt.Errorf("ocr: encode image: %w", err)
	}
	f.Close()
	return hopAOS(wslhost.Default(), execRunner, f.Name(), opts)
}
