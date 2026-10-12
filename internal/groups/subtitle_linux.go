package groups

import (
	"fmt"
	"strings"

	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/sys"
)

// showSubtitle picks the highest-fidelity tool installed. There is no single
// overlay primitive on Linux, so the ladder runs from a real borderless window
// down to a desktop notification, and each rung names itself rather than
// pretending to be the one above it.
func showSubtitle(req subtitleRequest) (string, error) {
	switch sys.FirstAvailable("yad", "zenity", "xmessage", "notify-send") {
	case "yad":
		return "overlay (yad)", subtitleYad(req)
	case "zenity":
		// zenity has no undecorated window; --notification is the closest it
		// gets, and it is a notification, not an overlay.
		return "notification (zenity)", sys.Spawn("zenity", "--notification",
			fmt.Sprintf("--timeout=%d", req.Seconds), "--text="+req.Text)
	case "xmessage":
		// xmessage takes focus and ignores --size; it is the last rung that
		// puts anything on screen, so it is offered with that stated.
		return "window (xmessage; takes focus, fixed size)", sys.Spawn("xmessage",
			"-center", "-bg", "black", "-fg", "white",
			"-timeout", fmt.Sprint(req.Seconds), req.Text)
	case "notify-send":
		_, err := sys.Output("notify-send", "-t", fmt.Sprint(req.Seconds*1000),
			"aos", req.Text)
		return "notification (notify-send)", err
	}
	return "", errUnsupported
}

func subtitleYad(req subtitleRequest) error {
	args := []string{
		"--text=" + fmt.Sprintf(`<span font_desc="Sans Bold %d" foreground="#ffffff">%s</span>`,
			req.Size*3/4, escapePango(req.Text)),
		"--text-align=center",
		"--no-buttons",
		"--undecorated",
		"--skip-taskbar",
		"--on-top",
		"--sticky",
		// The whole point: yad must not take the keyboard from the user.
		"--no-focus",
		"--timeout=" + fmt.Sprint(req.Seconds),
		"--timeout-indicator=none",
		"--class=aos-subtitle",
	}
	if geometry, ok := subtitleGeometry(req); ok {
		args = append(args, "--geometry="+geometry)
	} else {
		args = append(args, "--center")
	}
	// Spawn, never capture: yad owns a window until its timeout fires.
	return sys.Spawn("yad", args...)
}

// subtitleGeometry places the caption on the primary monitor. yad sizes itself
// to its text unless told otherwise, and a geometry needs a size to centre
// against, so the box is estimated from the glyph count — close enough to sit
// where it was asked to, and clamped so long text cannot run off the screen.
func subtitleGeometry(req subtitleRequest) (string, bool) {
	monitors, err := engine.ListMonitors()
	if err != nil || len(monitors) == 0 {
		return "", false
	}
	screen := monitors[0]
	for _, m := range monitors {
		if m.Primary {
			screen = m
			break
		}
	}

	width := len([]rune(req.Text))*req.Size*6/10 + 60
	if max := screen.Width - 80; width > max {
		width = max
	}
	if width < 200 {
		width = 200
	}
	height := req.Size*2 + 20

	x := screen.X + (screen.Width-width)/2
	y := screen.Y + 60
	switch req.Position {
	case "top":
		y = screen.Y + 60
	case "center":
		y = screen.Y + (screen.Height-height)/2
	default:
		y = screen.Y + screen.Height - height - 60
	}
	return fmt.Sprintf("%dx%d+%d+%d", width, height, x, y), true
}

// escapePango keeps caption text from being read as the markup that carries the
// font and colour around it.
func escapePango(value string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(value)
}
