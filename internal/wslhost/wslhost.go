// Package wslhost is how aos reaches Windows from inside WSL: detecting WSL,
// finding the Windows aos.exe, and translating paths with wslpath. The CLI
// uses it to send screen commands to the host; the ocr package uses it to
// run Windows OCR from a Linux build.
package wslhost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Deps is everything this package touches outside the process, so tests can
// fake WSL, cmd.exe, wslpath and aos.exe.
type Deps struct {
	Env      func(string) string
	Exists   func(path string) bool
	Output   func(dir, name string, args ...string) (string, error)
	LookPath func(name string) (string, error)
}

// Default is the real machine.
func Default() Deps {
	return Deps{
		Env:    os.Getenv,
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		Output: func(dir, name string, args ...string) (string, error) {
			cmd := exec.Command(name, args...)
			if dir != "" {
				cmd.Dir = dir
			}
			out, err := cmd.Output()
			return strings.TrimSpace(strings.ReplaceAll(string(out), "\r", "")), err
		},
		LookPath: exec.LookPath,
	}
}

// IsWSL reports whether this process runs inside WSL.
func IsWSL(env func(string) string, exists func(string) bool) bool {
	if env("WSL_DISTRO_NAME") != "" || env("WSL_INTEROP") != "" {
		return true
	}
	return exists != nil && exists("/proc/sys/fs/binfmt_misc/WSLInterop")
}

// ErrNoInterop means Windows programs cannot be started from this WSL.
var ErrNoInterop = errors.New("Windows programs cannot be started from here")

// FindAOS locates aos.exe from inside WSL: cached (when it still exists), then
// %LOCALAPPDATA%\aos (where install.sh puts it), %LOCALAPPDATA%\Programs\aos
// (install.cmd's default), then PATH. On failure tried lists where it looked.
func FindAOS(d Deps, cached string) (path string, tried []string, err error) {
	if cached != "" && d.Exists(cached) {
		return cached, nil, nil
	}
	// cmd.exe complains about a UNC working directory, so run it from C:.
	local, err := d.Output("/mnt/c", "cmd.exe", "/c", "echo %LOCALAPPDATA%")
	if err != nil || local == "" || strings.Contains(local, "%") {
		return "", nil, fmt.Errorf("%w (cmd.exe: %v)", ErrNoInterop, err)
	}
	if dir, err := d.Output("", "wslpath", "-u", local); err == nil && dir != "" {
		for _, candidate := range []string{
			filepath.Join(dir, "aos", "aos.exe"),
			filepath.Join(dir, "Programs", "aos", "aos.exe"),
		} {
			tried = append(tried, candidate)
			if d.Exists(candidate) {
				return candidate, tried, nil
			}
		}
	}
	if found, err := d.LookPath("aos.exe"); err == nil {
		return found, tried, nil
	}
	tried = append(tried, "aos.exe on PATH")
	return "", tried, errors.New("aos.exe not found")
}

// ToWindows converts a WSL path, which may not exist yet, to a Windows path.
// wslpath only converts paths that exist, so the parent is converted and the
// file name appended.
func ToWindows(d Deps, p string) (string, error) {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	if d.Exists(p) {
		if win, err := d.Output("", "wslpath", "-w", p); err == nil && win != "" {
			return win, nil
		}
	}
	dir, base := filepath.Split(p)
	win, err := d.Output("", "wslpath", "-w", filepath.Clean(dir))
	if err != nil || win == "" {
		return "", fmt.Errorf("cannot translate %s for Windows (wslpath: %v); does the folder exist?", p, err)
	}
	return strings.TrimRight(win, `\`) + `\` + base, nil
}
