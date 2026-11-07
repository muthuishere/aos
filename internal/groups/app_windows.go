package groups

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	appUser32                  = windows.NewLazySystemDLL("user32.dll")
	procAppEnumWindows         = appUser32.NewProc("EnumWindows")
	procAppIsWindowVisible     = appUser32.NewProc("IsWindowVisible")
	procAppGetWindowTextLength = appUser32.NewProc("GetWindowTextLengthW")
	procAppGetWindowThreadPID  = appUser32.NewProc("GetWindowThreadProcessId")
	procAppGetWindow           = appUser32.NewProc("GetWindow")
)

// installedApps reads the Uninstall registry keys, which is what Settings >
// Apps lists: machine-wide (64- and 32-bit views) and per-user.
func installedApps() ([]appInfo, error) {
	const uninstall = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall`
	roots := []struct {
		key  registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, uninstall},
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall`},
		{registry.CURRENT_USER, uninstall},
	}
	seen := map[string]bool{}
	var apps []appInfo
	for _, r := range roots {
		k, err := registry.OpenKey(r.key, r.path, registry.ENUMERATE_SUB_KEYS)
		if err != nil {
			continue
		}
		names, _ := k.ReadSubKeyNames(-1)
		k.Close()
		for _, name := range names {
			sub, err := registry.OpenKey(r.key, r.path+`\`+name, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			display, _, _ := sub.GetStringValue("DisplayName")
			icon, _, _ := sub.GetStringValue("DisplayIcon")
			loc, _, _ := sub.GetStringValue("InstallLocation")
			system, _, _ := sub.GetIntegerValue("SystemComponent")
			sub.Close()
			if display == "" || system == 1 || seen[strings.ToLower(display)] {
				continue
			}
			seen[strings.ToLower(display)] = true
			apps = append(apps, appInfo{Name: display, ID: name, Path: uninstallPath(icon, loc)})
		}
	}
	return apps, nil
}

// runningApps is the processes that own a visible, titled, unowned
// top-level window: the apps on the taskbar.
func runningApps() ([]appInfo, error) {
	pids := map[uint32]bool{}
	cb := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		if v, _, _ := procAppIsWindowVisible.Call(hwnd); v == 0 {
			return 1
		}
		if owner, _, _ := procAppGetWindow.Call(hwnd, 4 /* GW_OWNER */); owner != 0 {
			return 1
		}
		if n, _, _ := procAppGetWindowTextLength.Call(hwnd); n == 0 {
			return 1
		}
		var pid uint32
		procAppGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		pids[pid] = true
		return 1
	})
	procAppEnumWindows.Call(cb, 0)
	var apps []appInfo
	for pid := range pids {
		path := processImage(pid)
		if path == "" {
			continue
		}
		base := filepath.Base(path)
		apps = append(apps, appInfo{
			Name: strings.TrimSuffix(base, filepath.Ext(base)), ID: base,
			Path: path, PID: int(pid), Running: true,
		})
	}
	sortApps(apps)
	return apps, nil
}

func processImage(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}
