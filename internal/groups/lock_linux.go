package groups

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// screenLocked asks systemd-logind for this session's LockedHint, which the
// desktop's screen locker sets. Without logind the state is unknown.
func screenLocked() (bool, error) {
	id := os.Getenv("XDG_SESSION_ID")
	args := []string{"show-session"}
	if id != "" {
		args = append(args, id)
	}
	args = append(args, "--property=LockedHint", "--value")
	out, err := exec.Command("loginctl", args...).Output()
	if err != nil {
		return false, fmt.Errorf("lock state needs systemd-logind (loginctl): %v", err)
	}
	return strings.TrimSpace(string(out)) == "yes", nil
}
