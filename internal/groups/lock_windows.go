package groups

import (
	"encoding/binary"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	lockWtsapi32            = windows.NewLazySystemDLL("wtsapi32.dll")
	lockUser32              = windows.NewLazySystemDLL("user32.dll")
	procWTSQuerySessionInfo = lockWtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory       = lockWtsapi32.NewProc("WTSFreeMemory")
	procOpenInputDesktop    = lockUser32.NewProc("OpenInputDesktop")
	procCloseDesktop        = lockUser32.NewProc("CloseDesktop")
	procGetUserObjectInfoW  = lockUser32.NewProc("GetUserObjectInformationW")
)

const (
	wtsCurrentSession = 0xFFFFFFFF
	wtsSessionInfoEx  = 25 // WTS_INFO_CLASS WTSSessionInfoEx
	wtsStateLock      = 0  // WTS_SESSIONSTATE_LOCK
	wtsStateUnlock    = 1  // WTS_SESSIONSTATE_UNLOCK
	desktopSwitch     = 0x0100
	uoiName           = 2
)

// screenLocked reads this session's lock state from WTS (WTSSessionInfoEx,
// Windows 8 and later). If WTS will not say, it asks which desktop receives
// input: the lock screen runs on the Winlogon desktop, which an ordinary
// process cannot even open.
func screenLocked() (bool, error) {
	if locked, ok := wtsLocked(); ok {
		return locked, nil
	}
	return inputDesktopLocked()
}

func wtsLocked() (locked, ok bool) {
	var buf *byte
	var n uint32
	r, _, _ := procWTSQuerySessionInfo.Call(0, wtsCurrentSession, wtsSessionInfoEx,
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&n)))
	if r == 0 || buf == nil {
		return false, false
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	// WTSINFOEXW: Level uint32, then the level-1 data, which is 8-aligned (it
	// holds 64-bit times), so it starts at offset 8: SessionId, SessionState,
	// SessionFlags (int32) at 8, 12 and 16. Checked on Windows 11 arm64.
	if n < 20 {
		return false, false
	}
	b := unsafe.Slice(buf, int(n))
	if binary.LittleEndian.Uint32(b[0:]) != 1 {
		return false, false
	}
	switch int32(binary.LittleEndian.Uint32(b[16:])) {
	case wtsStateLock:
		return true, true
	case wtsStateUnlock:
		return false, true
	}
	return false, false
}

func inputDesktopLocked() (bool, error) {
	h, _, err := procOpenInputDesktop.Call(0, 0, desktopSwitch)
	if h == 0 {
		// Access denied to the input desktop: it is Winlogon's.
		if errno, ok := err.(windows.Errno); ok && errno == windows.ERROR_ACCESS_DENIED {
			return true, nil
		}
		return false, fmt.Errorf("cannot open the input desktop: %v", err)
	}
	defer procCloseDesktop.Call(h)
	var name [64]uint16
	var need uint32
	r, _, err := procGetUserObjectInfoW.Call(h, uoiName, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)*2), uintptr(unsafe.Pointer(&need)))
	if r == 0 {
		return false, fmt.Errorf("cannot name the input desktop: %v", err)
	}
	return windows.UTF16ToString(name[:]) != "Default", nil
}
