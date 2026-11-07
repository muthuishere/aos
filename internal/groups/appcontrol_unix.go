//go:build !windows

package groups

import (
	"os"

	"github.com/muthuishere/aos/internal/sys"
)

// ancestorPIDs is aos and every process above it (shell, terminal app).
func ancestorPIDs() map[int]bool {
	out, err := sys.Output("ps", "-axo", "pid=,ppid=")
	if err != nil {
		return map[int]bool{os.Getpid(): true, os.Getppid(): true}
	}
	return ancestorChain(os.Getpid(), parsePidPpid(out))
}
