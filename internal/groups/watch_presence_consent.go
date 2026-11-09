package groups

import "strings"

// Windows records every camera and microphone use in the per-user
// CapabilityAccessManager consent store (the data behind Settings > Privacy >
// Camera "last accessed"). Each app has LastUsedTimeStart and LastUsedTimeStop
// as FILETIMEs; while the app holds the device, Stop is 0 (or older than
// Start). The parsing lives here, free of the registry, so it is testable on
// any OS.

// consentEntry is one app's record under ConsentStore\<capability>.
type consentEntry struct {
	Key         string // subkey name
	NonPackaged bool   // a desktop (Win32) app: Key is its path with # for \
	Start, Stop uint64
}

// consentApps returns the display names of the apps that hold the device now.
func consentApps(entries []consentEntry) []string {
	var apps []string
	for _, e := range entries {
		if e.Start != 0 && e.Start > e.Stop {
			apps = append(apps, consentAppName(e))
		}
	}
	return uniqSorted(apps)
}

// consentAppName turns a subkey into something a person recognises: the exe
// name for a desktop app, the package name (without publisher id) otherwise.
func consentAppName(e consentEntry) string {
	if e.NonPackaged {
		path := strings.ReplaceAll(e.Key, "#", `\`)
		if i := strings.LastIndex(path, `\`); i >= 0 {
			path = path[i+1:]
		}
		return path
	}
	if i := strings.Index(e.Key, "_"); i > 0 {
		return e.Key[:i]
	}
	return e.Key
}
