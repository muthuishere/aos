package groups

import (
	"encoding/json"
	"fmt"

	"github.com/muthuishere/aos/internal/cli"
)

// writeJSON prints v as indented JSON, the shape every `--json` flag emits.
func writeJSON(c *cli.Ctx, v any) error {
	enc := json.NewEncoder(c.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// jsonOnly parses the args of a command whose only flag is --json.
func jsonOnly(route string, args []string) (bool, error) {
	set, err := parseArgs(args)
	if err != nil {
		return false, err
	}
	if err := set.Reject("json"); err != nil {
		return false, err
	}
	if len(set.Rest) > 0 {
		return false, fmt.Errorf("`%s` takes no positional arguments", route)
	}
	return set.Has("json"), nil
}

// humanBytes renders a byte count the way people read it: 16.0 GB, 512.0 MB.
// It uses binary multiples, which is what every OS's own tools show for RAM.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}
