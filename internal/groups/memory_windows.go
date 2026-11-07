package groups

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// memoryStatusEx is MEMORYSTATUSEX.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func readMemory() (memoryInfo, error) {
	var s memoryStatusEx
	s.Length = uint32(unsafe.Sizeof(s))
	if r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return memoryInfo{}, err
	}
	return windowsMemory(s.TotalPhys, s.AvailPhys, s.TotalPageFile, s.AvailPageFile), nil
}
