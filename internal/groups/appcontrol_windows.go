package groups

import (
	"os"
	"strconv"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine"
	"golang.org/x/sys/windows"
)

var (
	appctlUser32      = windows.NewLazySystemDLL("user32.dll")
	procAppShowWindow = appctlUser32.NewProc("ShowWindow")
	procAppPostMsg    = appctlUser32.NewProc("PostMessageW")
)

const (
	swMinimize = 6
	wmClose    = 0x0010
)

// showDesktop presses Win+D, the shell's own Show Desktop toggle.
func showDesktop() error { return engine.PressKey("win+d") }

func minimizeWindow(id string) error {
	hwnd, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return err
	}
	procAppShowWindow.Call(uintptr(hwnd), swMinimize)
	return nil
}

func closeWindow(id string) error {
	hwnd, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return err
	}
	if r, _, err := procAppPostMsg.Call(uintptr(hwnd), wmClose, 0, 0); r == 0 {
		return err
	}
	return nil
}

// ancestorPIDs walks the Toolhelp snapshot's parent links up from aos.
func ancestorPIDs() map[int]bool {
	parent := map[int]int{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err == nil {
		defer windows.CloseHandle(snap)
		var e windows.ProcessEntry32
		e.Size = uint32(unsafe.Sizeof(e))
		for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
			parent[int(e.ProcessID)] = int(e.ParentProcessID)
		}
	}
	return ancestorChain(os.Getpid(), parent)
}
