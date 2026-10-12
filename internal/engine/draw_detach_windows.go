//go:build windows

package engine

import (
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// detachProcess starts the child without a console and in its own
// process group, so closing the terminal does not end the drawing.
func detachProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}

// stopProcess asks the drawing to fade out and close (a stop marker it
// polls; Windows has no SIGTERM), then kills it if it is still there.
// Windows destroys its windows with the process either way.
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	marker := drawStopPath(pid)
	if os.WriteFile(marker, nil, 0o644) == nil {
		defer os.Remove(marker)
		for end := time.Now().Add(800 * time.Millisecond); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
			if !processAlive(pid) {
				return nil
			}
		}
	}
	return p.Kill()
}
