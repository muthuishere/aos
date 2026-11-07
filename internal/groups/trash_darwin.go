package groups

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/muthuishere/aos/internal/sys"
)

func readTrash() (trashInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return trashInfo{}, err
	}
	t, err := dirUsage(filepath.Join(home, ".Trash"))
	if err != nil {
		return trashInfo{}, fmt.Errorf("cannot read the Trash (%v); give this terminal Full Disk Access in System Settings > Privacy & Security", err)
	}
	// Items trashed from other volumes live on that volume.
	uid := strconv.Itoa(os.Getuid())
	vols, _ := filepath.Glob("/Volumes/*/.Trashes/" + uid)
	for _, v := range vols {
		if more, err := dirUsage(v); err == nil {
			t.Items += more.Items
			t.Bytes += more.Bytes
		}
	}
	return t, nil
}

// emptyTrash asks Finder, which empties the Trash on every volume and keeps
// its own bookkeeping straight.
func emptyTrash() error {
	_, err := sys.Osascript(`tell application "Finder" to empty trash`)
	return err
}
