//go:build darwin

package groups

// platformSearchEngines on macOS is Spotlight through mdfind.
func platformSearchEngines() []searchEngine {
	return []searchEngine{indexEngine("mdfind", "mdfind", func(q searchQuery) []string {
		args := []string{"-name", literalCore(q.Pattern)}
		if q.Root != "" {
			args = append(args, "-onlyin", q.Root)
		}
		return args
	})}
}
