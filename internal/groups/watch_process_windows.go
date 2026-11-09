//go:build windows

package groups

import (
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// watchListProcesses lists pid and name from one Toolhelp snapshot. The full
// listProcesses also samples CPU twice half a second apart and opens every
// process, which is far too heavy to run on every tick of a watcher.
func watchListProcesses() ([]processInfo, error) {
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
		found = append(found, processInfo{PID: int(entry.ProcessID), Name: name})
	}
	return found, nil
}

var (
	heldMu sync.Mutex
	held   = map[int]windows.Handle{}
)

// watchHoldProcess opens the process so Windows keeps its exit code readable
// after it ends. A process we cannot open simply has no exit code.
func watchHoldProcess(pid int) {
	heldMu.Lock()
	defer heldMu.Unlock()
	if _, ok := held[pid]; ok {
		return
	}
	if h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)); err == nil {
		held[pid] = h
	}
}

// watchProcessExitCode reads the exit code through the held handle.
func watchProcessExitCode(pid int) (int, bool) {
	heldMu.Lock()
	h, ok := held[pid]
	delete(held, pid)
	heldMu.Unlock()
	if !ok {
		return 0, false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if windows.GetExitCodeProcess(h, &code) != nil || code == 259 { // 259 = STILL_ACTIVE
		return 0, false
	}
	return int(code), true
}
