package groups

import "github.com/muthuishere/aos/internal/sys"

func listListening() ([]listenPort, error) {
	// lsof exits 1 when nothing matches, which is an empty answer, not a failure.
	out, err := sys.Output("lsof", "-nP", "-iTCP", "-sTCP:LISTEN", "-F", "pcn")
	if err != nil && out == "" && exitCodeOf(err) != 1 {
		return nil, err
	}
	return parseLsofListen(out), nil
}
