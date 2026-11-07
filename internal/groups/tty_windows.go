package groups

import (
	"os"

	"golang.org/x/sys/windows"
)

// stdinIsTerminal reports whether stdin is a console a person can answer on.
func stdinIsTerminal() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
}
