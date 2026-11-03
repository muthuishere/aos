//go:build windows

package groups

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	idleUser32           = windows.NewLazySystemDLL("user32.dll")
	procGetLastInputInfo = idleUser32.NewProc("GetLastInputInfo")
	procGetTickCount     = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount")
)

// userIdle: GetLastInputInfo, the tick of the last input in this session.
func userIdle() (time.Duration, bool) {
	info := struct{ CbSize, DwTime uint32 }{CbSize: 8}
	if r, _, _ := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		return 0, false
	}
	now, _, _ := procGetTickCount.Call()
	return time.Duration(uint32(now)-info.DwTime) * time.Millisecond, true
}
