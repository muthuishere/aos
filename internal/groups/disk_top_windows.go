//go:build windows

package groups

import (
	"io/fs"
	"os"
)

// Windows reports apparent size: the allocated size needs a file handle per
// file (GetFileInformationByHandleEx), too slow for a walk of a whole drive.
const sizeKind = "apparent"

// entrySize is the size of one entry without following links. A volume
// mounted inside a folder is rare on Windows, so every entry is device 0.
func entrySize(path string, e fs.DirEntry) (int64, uint64) {
	var info fs.FileInfo
	var err error
	if e != nil {
		info, err = e.Info()
	} else {
		info, err = os.Lstat(path)
	}
	if err != nil || info.IsDir() {
		return 0, 0
	}
	return info.Size(), 0
}
