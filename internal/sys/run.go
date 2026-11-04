// Package sys wraps the shelling-out every platform backend needs.
package sys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// DefaultTimeout bounds probe commands so a wedged tool cannot hang the CLI.
const DefaultTimeout = 20 * time.Second

// Has reports whether an executable is on PATH.
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Output runs a command and returns its trimmed stdout.
func Output(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	return OutputContext(ctx, name, args...)
}

// OutputContext is Output with a caller-supplied context.
func OutputContext(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Killing a process does not close a pipe that one of its descendants
	// inherited, and Run waits for the pipes, not the process. Without a
	// WaitDelay a launcher that hands its handles to a long-lived GUI app
	// wedges this call until the user closes that app. WaitDelay makes the
	// timeout mean what it says.
	cmd.WaitDelay = 2 * time.Second
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return strings.TrimSpace(stdout.String()), fmt.Errorf("%s: %s", name, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Quiet runs a command, discarding output, and reports only success.
func Quiet(name string, args ...string) bool {
	_, err := Output(name, args...)
	return err == nil
}

// Passthrough runs a command wired to the caller's stdio, for interactive tools.
func Passthrough(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// Spawn starts a program and returns without waiting for it.
//
// This is the right call for launching an application. Never capture a
// launcher's output: on Windows `cmd /c start` hands its inherited stdout
// handle to the app it spawns, so a captured Run blocks until that app exits —
// `launch notepad` would not return until Notepad was closed. Detaching from
// stdio entirely removes the possibility.
func Spawn(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the process so it does not linger as a zombie, without making the
	// caller wait for it.
	go func() { _ = cmd.Wait() }()
	return nil
}

// FirstAvailable returns the first name on PATH, or "" if none are.
func FirstAvailable(names ...string) string {
	for _, name := range names {
		if Has(name) {
			return name
		}
	}
	return ""
}

// Osascript runs an AppleScript one-liner (macOS).
func Osascript(script string) (string, error) {
	return Output("osascript", "-e", script)
}

// Shell returns the argv prefix that runs a command string through the
// platform's shell.
func Shell() []string {
	if runtime.GOOS == "windows" {
		return []string{"cmd", "/c"}
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return []string{shell, "-c"}
	}
	return []string{"sh", "-c"}
}

// PassthroughEnv is Passthrough with an explicit environment.
func PassthroughEnv(env []string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = env
	return cmd.Run()
}

// ExitCodeOf recovers a child process's exit status, or 0 when the failure was
// something else.
func ExitCodeOf(err error) int {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return 0
}
