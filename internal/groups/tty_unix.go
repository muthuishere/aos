//go:build !windows

package groups

import (
	"os"

	"golang.org/x/sys/unix"
)

// stdinIsTerminal reports whether a person can answer on stdin. A character
// device is not enough (/dev/null is one), so ask for terminal attributes.
func stdinIsTerminal() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), ioctlGetTermios)
	return err == nil
}
