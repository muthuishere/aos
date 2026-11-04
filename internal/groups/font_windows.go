package groups

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// fontRegistryKey maps installed fonts to their real display names, which the
// file stems do not give away: Candara Bold ships as `Candarab.ttf`.
const fontRegistryKey = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`

// fontTypeSuffix is the format tag Windows appends to a registry font name,
// as in "Arial (TrueType)".
var fontTypeSuffix = regexp.MustCompile(`\s*\((TrueType|OpenType|VGA res|All res)\)\s*$`)

func fontNames() ([]string, bool) {
	var names []string
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		k, err := registry.OpenKey(root, fontRegistryKey, registry.QUERY_VALUE)
		if err != nil {
			continue // HKCU has the key only when per-user fonts are installed
		}
		values, _ := k.ReadValueNames(-1)
		k.Close()
		for _, v := range values {
			if name := fontTypeSuffix.ReplaceAllString(strings.TrimSpace(v), ""); name != "" {
				names = append(names, name)
			}
		}
	}
	if len(names) == 0 {
		return nil, false
	}
	return names, true
}

func fontDirs() []string {
	var dirs []string
	if windir := os.Getenv("WINDIR"); windir != "" {
		dirs = append(dirs, filepath.Join(windir, "Fonts"))
	} else {
		dirs = append(dirs, `C:\Windows\Fonts`)
	}
	// Per-user fonts installed without admin rights live under LOCALAPPDATA.
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		dirs = append(dirs, filepath.Join(local, "Microsoft", "Windows", "Fonts"))
	}
	return dirs
}
