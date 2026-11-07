package groups

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	shell32               = windows.NewLazySystemDLL("shell32.dll")
	procSHQueryRecycleBin = shell32.NewProc("SHQueryRecycleBinW")
	procSHEmptyRecycleBin = shell32.NewProc("SHEmptyRecycleBinW")
)

// shQueryRBInfo is SHQUERYRBINFO. It is packed on 32-bit Windows but
// naturally aligned (cbSize padded to 8) on 64-bit, which this layout matches.
type shQueryRBInfo struct {
	CbSize   uint32
	_        uint32
	Size     int64
	NumItems int64
}

const (
	sherbNoConfirmation = 0x1
	sherbNoProgressUI   = 0x2
	sherbNoSound        = 0x4
)

// readTrash asks for the Recycle Bin on every drive at once (a nil root).
func readTrash() (trashInfo, error) {
	var q shQueryRBInfo
	q.CbSize = uint32(unsafe.Sizeof(q))
	if hr, _, _ := procSHQueryRecycleBin.Call(0, uintptr(unsafe.Pointer(&q))); hr != 0 {
		return trashInfo{}, windows.Errno(hr)
	}
	return trashInfo{Items: int(q.NumItems), Bytes: uint64(q.Size)}, nil
}

// emptyTrash empties every drive's Recycle Bin without Explorer's own dialog:
// aos has already asked.
func emptyTrash() error {
	hr, _, _ := procSHEmptyRecycleBin.Call(0, 0, sherbNoConfirmation|sherbNoProgressUI|sherbNoSound)
	// S_OK, or E_UNEXPECTED when the bin was already empty.
	if hr != 0 && uint32(hr) != 0x8000FFFF {
		return windows.Errno(hr)
	}
	return nil
}
