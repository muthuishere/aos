//go:build linux

package groups

import "os"

// platformSearchEngines on Linux is the locate database (plocate, then
// locate), then fd, each tried only when installed.
func platformSearchEngines() []searchEngine {
	locate := func(q searchQuery) []string { return []string{"-i", "-b", literalCore(q.Pattern)} }
	return []searchEngine{
		indexEngine("plocate", "plocate", locate),
		indexEngine("locate", "locate", locate),
		indexEngine("fd", "fd", func(q searchQuery) []string {
			args := []string{"-i", "--hidden", "--fixed-strings", "--absolute-path"}
			switch q.Kind {
			case "file":
				args = append(args, "-t", "f")
			case "dir":
				args = append(args, "-t", "d")
			}
			root := q.Root
			if root == "" {
				root = homeOrDot()
			}
			return append(args, literalCore(q.Pattern), root)
		}),
	}
}

func homeOrDot() string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return "."
}
