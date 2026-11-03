package groups

import (
	"strconv"
	"strings"
	"unsafe"

	"github.com/muthuishere/aos/internal/sys"
	"golang.org/x/sys/windows"
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

var procK32GetProcessMemoryInfo = windows.NewLazySystemDLL("kernel32.dll").NewProc("K32GetProcessMemoryInfo")

// listProcesses walks a Toolhelp snapshot. Windows has no cheap per-process
// CPU percentage, so the CPU column carries total CPU seconds; see
// processInfo. Processes we may not open (system, other users) are still
// listed, with zero CPU and memory.
func listProcesses() ([]processInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var found []processInfo
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(name[max(0, len(name)-4):], ".exe") {
			name = name[:len(name)-4]
		}
		p := processInfo{PID: int(entry.ProcessID), Name: name}
		cpu, bytes := processUsage(entry.ProcessID)
		p.CPU, p.MemoryMB = cpu, float64(bytes)/(1024*1024)
		found = append(found, p)
	}
	return found, nil
}

// processUsage is total CPU seconds and working-set bytes, zeros when the
// process cannot be opened.
func processUsage(pid uint32) (float64, uint64) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0, 0
	}
	defer windows.CloseHandle(h)
	var cpu float64
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) == nil {
		ticks := (uint64(kernel.HighDateTime)<<32 | uint64(kernel.LowDateTime)) +
			(uint64(user.HighDateTime)<<32 | uint64(user.LowDateTime))
		cpu = float64(ticks) / 1e7 // 100 ns units
	}
	var mem processMemoryCounters
	mem.CB = uint32(unsafe.Sizeof(mem))
	var ws uint64
	if r, _, _ := procK32GetProcessMemoryInfo.Call(uintptr(h), uintptr(unsafe.Pointer(&mem)), uintptr(mem.CB)); r != 0 {
		ws = uint64(mem.WorkingSetSize)
	}
	return cpu, ws
}

func killProcess(pid int, force bool) error {
	args := []string{"/PID", strconv.Itoa(pid)}
	if force {
		args = append(args, "/F")
	}
	_, err := sys.Output("taskkill", args...)
	return err
}
