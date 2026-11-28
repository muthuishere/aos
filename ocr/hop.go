package ocr

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/muthuishere/aos/internal/wslhost"
)

// runner starts exe with args and returns its stdout; tests fake it.
type runner func(exe string, args []string) ([]byte, error)

// hopAOS reads path through the Windows aos.exe from inside WSL: finds
// aos.exe, translates the path with wslpath, runs "aos.exe ocr <path> --json".
// Every failure to reach Windows wraps ErrUnsupported.
func hopAOS(d wslhost.Deps, run runner, path string, opts Options) ([]Line, error) {
	exe, tried, err := wslhost.FindAOS(d, d.Env("AOS_WINDOWS_EXE"))
	if err != nil {
		if len(tried) > 0 {
			err = fmt.Errorf("%v (tried %s)", err, strings.Join(tried, ", "))
		}
		return nil, fmt.Errorf("%w: inside WSL, OCR runs on Windows through aos.exe, but %v; install aos on Windows (install.cmd, or install.sh inside WSL)", ErrUnsupported, err)
	}
	win, err := wslhost.ToWindows(d, path)
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	args := []string{"ocr", win, "--json"}
	if len(opts.Languages) > 0 {
		args = append(args, "--lang="+strings.Join(opts.Languages, ","))
	}
	if opts.Fast {
		args = append(args, "--fast")
	}
	out, err := run(exe, args)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "unknown command") {
			return nil, fmt.Errorf("%w: the Windows aos.exe (%s) is too old for OCR; run: %s update", ErrUnsupported, exe, exe)
		}
		return nil, fmt.Errorf("ocr: aos.exe ocr failed: %v: %s", err, msg)
	}
	var lines []Line
	if err := json.Unmarshal(out, &lines); err != nil {
		return nil, fmt.Errorf("ocr: cannot read aos.exe output: %w", err)
	}
	return lines, nil
}
