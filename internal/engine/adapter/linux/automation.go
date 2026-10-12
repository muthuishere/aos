//go:build linux

// automation.go — screenshot + synthetic input for the Linux adapter.
//
// Same philosophy as adapter.go: shell out to the standard X11 tools
// rather than binding Xlib. Input goes through xdotool; capture tries
// ImageMagick `import` first (ubiquitous) and falls back to `scrot`.
// Wayland is best-effort exactly as it is for wmctrl.
package linux

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/engine/core"
)

func (a *Adapter) CaptureRect(bounds core.Rect, outPath string) error {
	geom := fmt.Sprintf("%dx%d+%d+%d", bounds.W, bounds.H, bounds.X, bounds.Y)
	if _, err := exec.LookPath("import"); err == nil {
		out, err := exec.Command("import", "-window", "root", "-crop", geom, outPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("import capture %s: %v: %s", geom, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if _, err := exec.LookPath("scrot"); err == nil {
		// scrot -a takes x,y,w,h and only writes to a fresh path.
		area := fmt.Sprintf("%d,%d,%d,%d", bounds.X, bounds.Y, bounds.W, bounds.H)
		out, err := exec.Command("scrot", "-o", "-a", area, outPath).CombinedOutput()
		if err != nil {
			return fmt.Errorf("scrot capture %s: %v: %s", area, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return fmt.Errorf("no screenshot tool found: install ImageMagick (import) or scrot: %w", core.ErrNotImplemented)
}

func (a *Adapter) MouseMove(x, y int) error {
	if err := exec.Command("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y)).Run(); err != nil {
		return fmt.Errorf("xdotool mousemove %d %d: %w", x, y, err)
	}
	return nil
}

func (a *Adapter) MouseClick(x, y int, button core.MouseButton, clicks int) error {
	if err := a.MouseMove(x, y); err != nil {
		return err
	}
	// xdotool button numbering: 1 left, 2 middle, 3 right.
	btn := "1"
	switch button {
	case core.MouseRight:
		btn = "3"
	case core.MouseMiddle:
		btn = "2"
	}
	args := []string{"click"}
	if clicks > 1 {
		args = append(args, "--repeat", strconv.Itoa(clicks), "--delay", "60")
	}
	args = append(args, btn)
	if err := exec.Command("xdotool", args...).Run(); err != nil {
		return fmt.Errorf("xdotool click %s: %w", btn, err)
	}
	return nil
}

func (a *Adapter) CursorPosition() (int, int, error) {
	out, err := exec.Command("xdotool", "getmouselocation", "--shell").Output()
	if err != nil {
		return 0, 0, fmt.Errorf("xdotool getmouselocation: %w", err)
	}
	x, y := 0, 0
	for _, line := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(line, "X="); ok {
			x, _ = strconv.Atoi(strings.TrimSpace(v))
		}
		if v, ok := strings.CutPrefix(line, "Y="); ok {
			y, _ = strconv.Atoi(strings.TrimSpace(v))
		}
	}
	return x, y, nil
}

func (a *Adapter) TypeText(text string) error {
	if err := exec.Command("xdotool", "type", "--delay", "12", "--", text).Run(); err != nil {
		return fmt.Errorf("xdotool type: %w", err)
	}
	return nil
}

// xdotoolKeyNames maps windowctl's normalized key names to X keysym
// names where they differ. Single characters and f1..f12 pass through
// unchanged (xdotool accepts them directly).
var xdotoolKeyNames = map[string]string{
	"enter": "Return", "tab": "Tab", "esc": "Escape", "space": "space",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"delete": "Delete", "backspace": "BackSpace",
	"home": "Home", "end": "End", "pageup": "Page_Up", "pagedown": "Page_Down",
}

func (a *Adapter) PressChord(chord core.Chord) error {
	key := chord.Key
	if mapped, ok := xdotoolKeyNames[key]; ok {
		key = mapped
	} else if strings.HasPrefix(key, "f") && len(key) > 1 {
		key = strings.ToUpper(key[:1]) + key[1:] // f5 → F5
	}
	var parts []string
	if chord.Cmd {
		parts = append(parts, "super")
	}
	if chord.Ctrl {
		parts = append(parts, "ctrl")
	}
	if chord.Alt {
		parts = append(parts, "alt")
	}
	if chord.Shift {
		parts = append(parts, "shift")
	}
	parts = append(parts, key)
	combo := strings.Join(parts, "+")
	if err := exec.Command("xdotool", "key", combo).Run(); err != nil {
		return fmt.Errorf("xdotool key %s: %w", combo, err)
	}
	return nil
}

// Launch execs the app name directly (detached); Linux has no
// LaunchServices equivalent that resolves pretty names, so the app
// argument is a binary on PATH (or a full path).
func (a *Adapter) Launch(app string) error {
	cmd := exec.Command(app)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %q: %w", app, err)
	}
	// Detach: don't hold a zombie; the process outlives engine.
	go func() { _ = cmd.Wait() }()
	return nil
}

// No Screen Recording-style permission gate exists on X11; captures
// either work or the tool errors. Mirrors CheckAccessibility's no-op
// rationale.
func (a *Adapter) CheckScreenCapture() bool    { return true }
func (a *Adapter) RequestScreenCapture() error { return nil }
