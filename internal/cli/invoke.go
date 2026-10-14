package cli

import (
	"bytes"
	"io"
	"strings"
)

// Result is the outcome of one programmatic command invocation.
type Result struct {
	Exit   int    `json:"exit"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

// Invoke runs argv against the registry with output captured instead of
// streamed. It is how the HTTP and MCP servers reuse the exact command tree the
// terminal uses, so a route behaves identically however an agent reaches it.
func Invoke(base *Ctx, argv []string, stdin string) Result {
	var stdout, stderr bytes.Buffer
	ctx := &Ctx{
		Registry:   base.Registry,
		Stdin:      io.NopCloser(strings.NewReader(stdin)),
		Stdout:     &stdout,
		Stderr:     &stderr,
		Env:        base.Env,
		GOOS:       base.GOOS,
		Version:    base.Version,
		Source:     "mcp",
		Recorder:   base.Recorder,
		HostDeps:   base.HostDeps,
		Local:      true,
		ScreenOnly: base.ScreenOnly,
	}
	exit := Run(ctx, argv)
	return Result{Exit: exit, Stdout: stdout.String(), Stderr: stderr.String()}
}
