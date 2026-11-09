//go:build !windows

package groups

import (
	"io/fs"
	"os"
	"syscall"
)

const sizeKind = "allocated"

// entrySize is the allocated size (st_blocks * 512) and the device of one
// entry, without following symlinks. e may be nil.
func entrySize(path string, e fs.DirEntry) (int64, uint64) {
	var info fs.FileInfo
	var err error
	if e != nil {
		info, err = e.Info()
	} else {
		info, err = os.Lstat(path)
	}
	if err != nil {
		return 0, 0
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int64(st.Blocks) * 512, uint64(st.Dev)
	}
	return info.Size(), 0
}
