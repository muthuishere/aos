package groups

import (
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows"
)

// Laptop panels answer the video brightness IOCTLs on \\.\LCD; external
// monitors answer DDC/CI through dxva2. Neither needs WMI or a shell.
const (
	ioctlVideoQueryDisplayBrightness = 0x00230498
	ioctlVideoSetDisplayBrightness   = 0x0023049C
	displayPolicyBoth                = 3
	monitorDefaultToPrimary          = 1
)

type displayBrightness struct{ Policy, AC, DC byte }

type physicalMonitor struct {
	Handle      windows.Handle
	Description [128]uint16
}

var (
	dxva2                       = windows.NewLazySystemDLL("dxva2.dll")
	procGetPhysicalMonitors     = dxva2.NewProc("GetPhysicalMonitorsFromHMONITOR")
	procGetMonitorBrightness    = dxva2.NewProc("GetMonitorBrightness")
	procSetMonitorBrightness    = dxva2.NewProc("SetMonitorBrightness")
	procDestroyPhysicalMonitors = dxva2.NewProc("DestroyPhysicalMonitors")
	procMonitorFromPoint        = windows.NewLazySystemDLL("user32.dll").NewProc("MonitorFromPoint")
)

var errNoBrightness = &cli.ExitError{Code: 2, Message: "this display exposes no brightness control (no laptop panel, and the monitor does not answer DDC/CI)"}

func openLCD() (windows.Handle, error) {
	p, _ := windows.UTF16PtrFromString(`\\.\LCD`)
	return windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
}

// ddcBrightness opens the primary monitor over DDC/CI and returns its handle
// and range; the caller must release it with destroyMonitor.
func ddcBrightness() (pm physicalMonitor, min, cur, max uint32, ok bool) {
	hmon, _, _ := procMonitorFromPoint.Call(0, monitorDefaultToPrimary)
	if r, _, _ := procGetPhysicalMonitors.Call(hmon, 1, uintptr(unsafe.Pointer(&pm))); r == 0 {
		return pm, 0, 0, 0, false
	}
	r, _, _ := procGetMonitorBrightness.Call(uintptr(pm.Handle),
		uintptr(unsafe.Pointer(&min)), uintptr(unsafe.Pointer(&cur)), uintptr(unsafe.Pointer(&max)))
	if r == 0 || max <= min {
		destroyMonitor(&pm)
		return pm, 0, 0, 0, false
	}
	return pm, min, cur, max, true
}

func destroyMonitor(pm *physicalMonitor) {
	procDestroyPhysicalMonitors.Call(1, uintptr(unsafe.Pointer(pm)))
}

func getBrightness() (int, error) {
	if h, err := openLCD(); err == nil {
		defer windows.CloseHandle(h)
		var b displayBrightness
		var n uint32
		if windows.DeviceIoControl(h, ioctlVideoQueryDisplayBrightness, nil, 0,
			(*byte)(unsafe.Pointer(&b)), uint32(unsafe.Sizeof(b)), &n, nil) == nil {
			return int(b.AC), nil
		}
	}
	pm, min, cur, max, ok := ddcBrightness()
	if !ok {
		return 0, errNoBrightness
	}
	destroyMonitor(&pm)
	return toPercent(int(cur-min), int(max-min)), nil
}

func setBrightness(pct int) error {
	if h, err := openLCD(); err == nil {
		defer windows.CloseHandle(h)
		b := displayBrightness{Policy: displayPolicyBoth, AC: byte(pct), DC: byte(pct)}
		var n uint32
		if windows.DeviceIoControl(h, ioctlVideoSetDisplayBrightness,
			(*byte)(unsafe.Pointer(&b)), uint32(unsafe.Sizeof(b)), nil, 0, &n, nil) == nil {
			return nil
		}
	}
	pm, min, _, max, ok := ddcBrightness()
	if !ok {
		return errNoBrightness
	}
	defer destroyMonitor(&pm)
	level := uint32(fromPercent(pct, int(max-min))) + min
	if r, _, _ := procSetMonitorBrightness.Call(uintptr(pm.Handle), uintptr(level)); r == 0 {
		return errNoBrightness
	}
	return nil
}
