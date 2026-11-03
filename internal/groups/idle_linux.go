//go:build linux

package groups

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// userIdle: xprintidle (XScreenSaver extension) on X11. Without it (or
// on Wayland) the idle time is unknown and the polite guard lets input
// through; `aos doctor` says so.
func userIdle() (time.Duration, bool) {
	out, err := exec.Command("xprintidle").Output()
	if err != nil {
		return 0, false
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(ms) * time.Millisecond, true
}
