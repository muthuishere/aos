package cli

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/muthuishere/aos/internal/obs"
)

// Ctx is the execution context handed to every Runner.
type Ctx struct {
	Registry *Registry
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Env      func(string) string
	GOOS     string
	Version  string
	// Source names how this invocation arrived — "cli", "mcp" — so telemetry
	// can tell a person at a terminal apart from an agent calling a tool.
	Source string
	// Recorder is where spans go. Nil means telemetry is off; every call on it
	// is a no-op, so nothing branches on it.
	Recorder *obs.Recorder
	// HostDeps is how host resolution reaches WSL, wslpath and aos.exe. Nil
	// means the real ones; tests swap in fakes.
	HostDeps *HostDeps
	// Local pins every command to this machine. It is set for calls arriving
	// over MCP: the server is the host, so it never delegates again.
	Local bool
	// ScreenOnly refuses every command that does not follow the host. It is
	// set for calls arriving over the SSH socket.
	ScreenOnly bool
}

// NewCtx builds a context bound to the real process.
func NewCtx(reg *Registry, version string) *Ctx {
	return &Ctx{
		Registry: reg,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Env:      os.Getenv,
		GOOS:     runtime.GOOS,
		Version:  version,
		Source:   "cli",
		Recorder: obs.NewRecorder(os.Getenv, version),
	}
}

func (c *Ctx) Printf(format string, a ...any) { fmt.Fprintf(c.Stdout, format, a...) }
func (c *Ctx) Println(a ...any)               { fmt.Fprintln(c.Stdout, a...) }
func (c *Ctx) Warnf(format string, a ...any)  { fmt.Fprintf(c.Stderr, format, a...) }

// ErrUnsupported is returned when a command exists but not for this platform.
type ErrUnsupported struct {
	Route string
	GOOS  string
	On    []string
}

func (e *ErrUnsupported) Error() string {
	if len(e.On) == 0 {
		return fmt.Sprintf("%q is not supported on %s", e.Route, e.GOOS)
	}
	return fmt.Sprintf("%q is not supported on %s (available on: %v)", e.Route, e.GOOS, e.On)
}
