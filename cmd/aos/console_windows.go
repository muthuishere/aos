//go:build windows

package main

import (
	"os"

	windowsadapter "github.com/muthuishere/aos/internal/engine/adapter/windows"
)

// The console helper: aos re-executed with no console to attach to a
// terminal's console and type into / read it (see adapter/windows/console.go).
func init() {
	if len(os.Args) > 1 && os.Args[1] == windowsadapter.ConsoleHelperArg {
		os.Exit(windowsadapter.ConsoleHelperMain(os.Args[2:], os.Stdin, os.Stdout, os.Stderr))
	}
}
