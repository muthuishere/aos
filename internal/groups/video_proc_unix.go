//go:build !windows

package groups

import (
	"os"
	"os/exec"
	"syscall"
)

// detach puts a child in its own session so it outlives the CLI and the
// terminal that started it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// hideChild is a no-op off Windows.
func hideChild(*exec.Cmd) {}

func videoProcAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// interrupt asks ffmpeg to finish the file (SIGINT finalizes an mp4).
func interrupt(p *os.Process) error { return p.Signal(os.Interrupt) }
