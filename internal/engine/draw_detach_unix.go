//go:build !windows

package engine

import (
	"os"
	"os/exec"
	"syscall"
)

// detachProcess puts the child in its own session so it outlives the
// terminal / agent shell that started it.
func detachProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	return err == nil && p.Signal(syscall.Signal(0)) == nil
}

// stopProcess asks the drawing to close cleanly (SIGTERM → Stop).
func stopProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
