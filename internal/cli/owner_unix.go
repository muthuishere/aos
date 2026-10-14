//go:build !windows

package cli

import (
	"os"
	"syscall"
)

// ownedByMe reports whether the file belongs to the current user.
func ownedByMe(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
