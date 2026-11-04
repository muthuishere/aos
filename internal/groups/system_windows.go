package groups

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/sys"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func systemLock() error {
	_, err := sys.Output("rundll32.exe", "user32.dll,LockWorkStation")
	return err
}

func systemSleep() error {
	// The third argument disables wake timers; hibernation stays off so this is
	// a true suspend-to-RAM on machines with hibernation disabled.
	_, err := sys.Output("rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0")
	return err
}

func systemRestart() error {
	_, err := sys.Output("shutdown", "/r", "/t", "0")
	return err
}

func systemShutdown() error {
	_, err := sys.Output("shutdown", "/s", "/t", "0")
	return err
}

func systemLogout() error {
	_, err := sys.Output("shutdown", "/l")
	return err
}

// systemInfo reads the registry and the kernel directly: no WMI, no shell.
func systemInfo() (map[string]string, error) {
	facts := map[string]string{"platform": "windows"}
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE); err == nil {
		name, _, _ := k.GetStringValue("ProductName")
		build, _, _ := k.GetStringValue("CurrentBuildNumber")
		k.Close()
		// Windows 11 still writes "Windows 10" into ProductName.
		if n, err := strconv.Atoi(build); err == nil && n >= 22000 {
			name = strings.Replace(name, "Windows 10", "Windows 11", 1)
		}
		if name != "" {
			facts["os"] = "Microsoft " + name
		}
		if build != "" {
			facts["build"] = build
		}
	}
	if v := windows.RtlGetVersion(); v != nil {
		facts["os_version"] = fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
		facts["build"] = strconv.Itoa(int(v.BuildNumber))
	}
	if a := os.Getenv("PROCESSOR_ARCHITECTURE"); a != "" {
		facts["arch"] = a
	} else {
		facts["arch"] = runtime.GOARCH
	}
	if h, err := os.Hostname(); err == nil {
		facts["host"] = h
	}
	if cpu := regString(`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, "ProcessorNameString"); cpu != "" {
		facts["cpu"] = strings.TrimSpace(cpu)
	}
	maker := regString(`HARDWARE\DESCRIPTION\System\BIOS`, "SystemManufacturer")
	model := regString(`HARDWARE\DESCRIPTION\System\BIOS`, "SystemProductName")
	if m := strings.TrimSpace(maker + " " + model); m != "" {
		facts["model"] = m
	}
	return facts, nil
}

// regString reads one HKLM string value, "" when absent.
func regString(path, name string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue(name)
	return v
}
