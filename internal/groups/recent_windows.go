package groups

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// listRecent reads the shortcuts Explorer drops in %APPDATA%\Microsoft\
// Windows\Recent, newest first. They do not record which app opened the
// file (that lives in the per-app Jump List compound files), so --app is
// not offered here.
func listRecent(app string) ([]recentFile, error) {
	if app != "" {
		return nil, &cli.ExitError{Code: 2, Message: "--app is not supported on Windows: the Recent folder does not record which app opened a file"}
	}
	dir := filepath.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Recent")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []recentFile
	for _, e := range entries {
		if !strings.EqualFold(filepath.Ext(e.Name()), ".lnk") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		target := lnkTarget(data)
		if target == "" {
			continue
		}
		if st, err := os.Stat(target); err != nil || st.IsDir() {
			continue // folders are recent too, but this lists documents
		}
		out = append(out, recentFile{Path: target, when: info.ModTime()})
	}
	newestFirst(out)
	return out, nil
}
