//go:build darwin

package groups

import (
	"os/exec"
	"regexp"
	"strconv"
	"time"
)

var hidIdleRe = regexp.MustCompile(`"HIDIdleTime" = (\d+)`)

// userIdle reads IOHIDSystem's HIDIdleTime (nanoseconds since the last
// keyboard / mouse event).
func userIdle() (time.Duration, bool) {
	out, err := exec.Command("ioreg", "-c", "IOHIDSystem", "-d", "4", "-r", "-k", "HIDIdleTime").Output()
	if err != nil {
		return 0, false
	}
	m := hidIdleRe.FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(string(m[1]), 10, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(n), true
}
