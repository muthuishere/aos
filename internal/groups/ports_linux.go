package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// listListening asks ss (iproute2, on every modern distro), then lsof. Other
// users' sockets show without a pid unless aos runs as root.
func listListening() ([]listenPort, error) {
	if sys.Has("ss") {
		out, err := sys.Output("ss", "-Hltnp")
		if err != nil {
			return nil, err
		}
		return parseSSListen(out), nil
	}
	if sys.Has("lsof") {
		out, err := sys.Output("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn")
		if err != nil && out == "" && exitCodeOf(err) != 1 {
			return nil, err
		}
		return parseLsofListen(out), nil
	}
	return nil, &cli.ExitError{Code: 2, Message: "listing ports needs `ss` (iproute2) or `lsof`; install one"}
}
