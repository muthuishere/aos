package groups

import (
	"os"
	"path/filepath"
)

// trashDir is the freedesktop.org home trash: $XDG_DATA_HOME/Trash.
func trashDir() (string, error) {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "Trash"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "Trash"), nil
}

func readTrash() (trashInfo, error) {
	dir, err := trashDir()
	if err != nil {
		return trashInfo{}, err
	}
	return dirUsage(filepath.Join(dir, "files"))
}

// emptyTrash clears files/ and their info/ records together, plus the size
// cache, as file managers do.
func emptyTrash() error {
	dir, err := trashDir()
	if err != nil {
		return err
	}
	for _, sub := range []string{"files", "info"} {
		if err := clearDir(filepath.Join(dir, sub)); err != nil {
			return err
		}
	}
	_ = os.Remove(filepath.Join(dir, "directorysizes"))
	return nil
}
