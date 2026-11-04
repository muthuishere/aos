package groups

import (
	"strings"
	"unsafe"

	"github.com/muthuishere/aos/internal/sys"
	"golang.org/x/sys/windows"
)

func readNetwork() (netState, error) {
	var state netState

	state.Interface, state.IP = defaultRoute()

	// netsh is the only reliable SSID source; it prints nothing useful when no
	// wireless adapter exists, so a failure here is not an error overall.
	if wlan, err := sys.Output("netsh", "wlan", "show", "interfaces"); err == nil {
		for _, line := range strings.Split(wlan, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			switch key {
			case "SSID":
				state.SSID = value
			case "Signal":
				state.Signal = value
			}
		}
	}
	return state, nil
}

var procGetBestInterface = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetBestInterface")

// defaultRoute is the adapter Windows would use to reach the internet, and
// its first IPv4 address, from the IP helper API.
func defaultRoute() (string, string) {
	var index uint32
	dest := uint32(8) | uint32(8)<<8 | uint32(8)<<16 | uint32(8)<<24 // 8.8.8.8, network order
	if r, _, _ := procGetBestInterface.Call(uintptr(dest), uintptr(unsafe.Pointer(&index))); r != 0 {
		return "", ""
	}
	size := uint32(15000)
	for i := 0; i < 3; i++ {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_INET, windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return "", ""
		}
		for a := first; a != nil; a = a.Next {
			if a.IfIndex != index {
				continue
			}
			ip := ""
			for u := a.FirstUnicastAddress; u != nil; u = u.Next {
				if v4 := u.Address.IP().To4(); v4 != nil {
					ip = v4.String()
					break
				}
			}
			return windows.UTF16PtrToString(a.FriendlyName), ip
		}
		return "", ""
	}
	return "", ""
}
