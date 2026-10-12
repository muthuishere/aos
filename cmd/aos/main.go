// Command aos is a cross-platform command center for the machine you are
// on — the omarchy CLI's shape, running natively on macOS, Windows, and Linux.
package main

import (
	"os"
	"runtime/debug"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/groups"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// resolveVersion falls back to the version Go stamps into the binary.
//
// `go install ...@latest` is the documented way to get this, and it cannot
// pass ldflags -- so the recommended install produced a binary that called
// itself "dev" while knowing perfectly well it was v0.2.0. The build info
// carries the module version for exactly this case; it reads "(devel)" for a
// local build, where the ldflags value is the better answer anyway.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return info.Main.Version
}

func main() {
	registry := cli.NewRegistry()
	groups.Register(registry)

	ctx := cli.NewCtx(registry, resolveVersion())
	// A virtual display started by `headless start` is this machine's display
	// as far as the GUI commands are concerned, so adopt it before anything
	// checks whether one exists.
	groups.AdoptManagedDisplay(ctx.Env)
	// User extensions, lowest precedence first is irrelevant here: both refuse
	// to shadow a builtin, so the shipped commands always mean what they say.
	cli.LoadAdapters(registry, ctx.Env)
	cli.DiscoverPlugins(registry, ctx.Env)

	groups.EnsureSkill(ctx, os.Args[1:])
	os.Exit(cli.Run(ctx, os.Args[1:]))
}
