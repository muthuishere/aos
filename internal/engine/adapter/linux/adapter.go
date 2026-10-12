//go:build linux

// Package linux is the Linux platform adapter for windowctl.
//
// For the os-spikes tracer bullet this implementation shells out to wmctrl
// (and xdotool for focus); native X11 via Xlib is a planned follow-up.
package linux

import (
	"bufio"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/engine/core"
)

type Adapter struct{}

func New() *Adapter { return &Adapter{} }

func (a *Adapter) ListWindows() ([]core.Window, error) {
	out, err := exec.Command("wmctrl", "-lpG").Output()
	if err != nil {
		return nil, fmt.Errorf("wmctrl -lpG: %w", err)
	}
	var ws []core.Window
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		pid, _ := strconv.Atoi(fields[2])
		x, _ := strconv.Atoi(fields[3])
		y, _ := strconv.Atoi(fields[4])
		w, _ := strconv.Atoi(fields[5])
		h, _ := strconv.Atoi(fields[6])
		host := fields[7]
		title := strings.Join(fields[8:], " ")
		ws = append(ws, core.Window{
			ID:     fields[0],
			Title:  title,
			App:    host,
			PID:    pid,
			Bounds: core.Rect{X: x, Y: y, W: w, H: h},
		})
	}
	return ws, scanner.Err()
}

func (a *Adapter) ListMonitors() ([]core.Monitor, error) {
	out, err := exec.Command("xrandr", "--listmonitors").Output()
	if err != nil {
		return nil, fmt.Errorf("xrandr --listmonitors: %w", err)
	}
	var ms []core.Monitor
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	id := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.Contains(line, "/") || !strings.Contains(line, "+") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		geom := fields[2]
		parts := strings.Split(geom, "+")
		if len(parts) != 3 {
			continue
		}
		dim := strings.Split(parts[0], "x")
		if len(dim) != 2 {
			continue
		}
		w, _ := strconv.Atoi(stripUnit(dim[0]))
		h, _ := strconv.Atoi(stripUnit(dim[1]))
		x, _ := strconv.Atoi(parts[1])
		y, _ := strconv.Atoi(parts[2])
		ms = append(ms, core.Monitor{
			ID:      id,
			X:       x,
			Y:       y,
			Width:   w,
			Height:  h,
			Primary: strings.HasPrefix(fields[1], "+*"),
		})
		id++
	}
	return ms, scanner.Err()
}

func (a *Adapter) Move(id string, b core.Rect) error {
	arg := fmt.Sprintf("0,%d,%d,%d,%d", b.X, b.Y, b.W, b.H)
	if err := exec.Command("wmctrl", "-i", "-r", id, "-e", arg).Run(); err != nil {
		return fmt.Errorf("wmctrl move %s %s: %w", id, arg, err)
	}
	return nil
}

func (a *Adapter) Focus(id string) error {
	if err := exec.Command("wmctrl", "-i", "-a", id).Run(); err != nil {
		return fmt.Errorf("wmctrl focus %s: %w", id, err)
	}
	return nil
}

// RequestAccessibility is a no-op on Linux. X11 / wmctrl do not require
// a per-process trust grant comparable to macOS Accessibility — the
// permission model is the display-server connection itself, which
// either works or fails on the underlying calls. The CLI surface keeps
// the method on the interface so `windowctl permissions` remains
// available cross-platform; here it just returns nil and the CLI
// prints a "not required" message based on runtime.GOOS.
func (a *Adapter) RequestAccessibility() error { return nil }

// CheckAccessibility is a no-op on Linux for the same reason
// RequestAccessibility is — there's no macOS-style per-process AX
// trust gate on X11. Returns true so callers that branch on the result
// see the "no setup required" answer. The CLI's `permissions --status`
// path on linux ignores this value and prints a platform-derived
// "not required on linux" message regardless.
func (a *Adapter) CheckAccessibility() bool { return true }

func stripUnit(s string) string {
	for i, r := range s {
		if r < '0' || r > '9' {
			return s[:i]
		}
	}
	return s
}
