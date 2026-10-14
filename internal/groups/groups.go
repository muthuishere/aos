// Package groups holds every built-in command group and its per-platform
// backends. A group's cross-platform file declares the commands; the
// _darwin.go / _windows.go / _linux.go files supply the machine-specific work.
package groups

import (
	"errors"

	"github.com/muthuishere/aos/internal/cli"
)

// errUnsupported is what a platform backend returns for work it cannot do.
// Dispatch turns a command's declared Platforms into a friendlier message, so
// this is the fallback for a capability missing at runtime rather than by design.
var errUnsupported = errors.New("not supported on this platform")

// registrar is implemented by each group file via an init-time append.
var registrars []func(*cli.Registry)

func register(fn func(*cli.Registry)) { registrars = append(registrars, fn) }

// guiGroups drive the desktop: windows, pointer, keyboard, screen. Every
// command in them needs a display server.
var guiGroups = map[string]bool{
	"capture":    true,
	"display":    true,
	"draw":       true,
	"find":       true,
	"key":        true,
	"launch":     true,
	"mouse":      true,
	"open":       true,
	"permission": true,
	"remote":     true, // sharing a screen needs a screen
	"subtitle":   true,
	"tab":        true,
	// The whole watch group is GUI, not just `watch window`: a clipboard
	// watcher on a headless machine would spin for the life of the process,
	// warning every tick, which is far worse than one clear refusal.
	"watch":  true,
	"webapp": true,
	"window": true,
}

// guiRoutes are the individual commands in otherwise screenless groups that
// still need a session. Sleeping and rebooting work on a headless box; locking
// a screen that does not exist does not.
var guiRoutes = map[string]bool{
	"system lock":   true,
	"system logout": true,
}

// Register wires every built-in group into the registry, then stamps the
// display requirement in one place so a new command in a GUI group cannot
// forget it.
func Register(r *cli.Registry) {
	for _, fn := range registrars {
		fn(r)
	}
	for _, cmd := range r.Commands() {
		if guiGroups[cmd.Group] || guiRoutes[cmd.Route()] {
			cmd.NeedsDisplay = true
		}
	}
}
