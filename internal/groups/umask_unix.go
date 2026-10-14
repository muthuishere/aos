//go:build !windows

package groups

import "syscall"

// syscallUmask sets the process umask and returns the previous one, so a
// socket is never briefly reachable by others between creation and chmod.
func syscallUmask(mask int) int { return syscall.Umask(mask) }
