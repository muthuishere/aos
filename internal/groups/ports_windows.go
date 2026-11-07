package groups

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const tcpTableOwnerPIDListener = 3

// listListening reads the kernel's TCP tables (IPv4 and IPv6) filtered to
// listeners, with the owning pid, and names each pid from the process list.
func listListening() ([]listenPort, error) {
	names := map[int]string{}
	if procs, err := listProcesses(); err == nil {
		for _, p := range procs {
			names[p.PID] = p.Name
		}
	}
	var found []listenPort
	for _, family := range []uint32{windows.AF_INET, windows.AF_INET6} {
		rows, err := tcpListeners(family)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			r.Process = names[r.PID]
			found = append(found, r)
		}
	}
	return found, nil
}

func tcpListeners(family uint32) ([]listenPort, error) {
	var size uint32
	procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), tcpTableOwnerPIDListener, 0)
	if r != 0 {
		return nil, windows.Errno(r)
	}
	return parseTCPTable(buf, family == windows.AF_INET6), nil
}
