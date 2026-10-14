package groups

import (
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

// Every shipped command must say whether it runs on the host (the screen's
// machine) or always locally. A new group that skips hostTable fails here
// instead of silently acting on the wrong machine under WSL or SSH.
func TestEveryCommandIsClassifiedForTheHost(t *testing.T) {
	r := cli.NewRegistry()
	Register(r)
	for _, cmd := range r.Commands() {
		if _, known := cli.HostSide(cmd.Route()); !known {
			t.Errorf("%s: not in hostTable (internal/cli/host.go); decide whether it follows the screen", cmd.Route())
		}
	}
	for _, route := range []string{"system logout", "system restart", "system shutdown"} {
		if follows, _ := cli.HostSide(route); follows {
			t.Errorf("%s must never be forwarded", route)
		}
	}
}
