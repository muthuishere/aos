package groups

import (
	"os"
	"path/filepath"
)

func listRecent(app string) ([]recentFile, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".local", "share")
	}
	data, err := os.ReadFile(filepath.Join(base, "recently-used.xbel"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseXBEL(data, app)
}
