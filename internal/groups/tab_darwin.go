//go:build darwin

package groups

// macOS: the scripted families first, then the accessibility walk for any
// other app that shows a tab strip.
func init() {
	tabProviders = func() []tabProvider {
		scripted := []*scriptFamily{chromeFamily(), safariFamily(), ghosttyFamily(), itermFamily(), terminalFamily()}
		out := make([]tabProvider, 0, len(scripted)+1)
		for _, f := range scripted {
			out = append(out, f)
		}
		out = append(out, newAXTabs("ax", "cmd+w", func(app string) bool {
			for _, f := range scripted {
				if _, ok := f.apps[f.scripted(app)]; ok {
					return true
				}
			}
			return false
		}))
		return out
	}
}
