//go:build windows

package groups

import "strconv"

// platformSearchEngines on Windows is Everything's es.exe when it is on
// PATH. The Windows Search (OLE DB) tier is not built yet; the walk follows.
func platformSearchEngines() []searchEngine {
	return []searchEngine{indexEngine("everything", "es.exe", func(q searchQuery) []string {
		// Ask for more than the limit: the collector drops path-only matches.
		args := []string{"-n", strconv.Itoa(q.Limit * 4)}
		if q.Root != "" {
			args = append(args, "-path", q.Root)
		}
		switch q.Kind {
		case "file":
			args = append(args, "/a-d")
		case "dir":
			args = append(args, "/ad")
		}
		return append(args, q.Pattern)
	})}
}
