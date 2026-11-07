package groups

import (
	"os"
	"path/filepath"
)

// desktopDirs are the XDG application folders, user first so a user's own
// override of a system .desktop file wins.
func desktopDirs() []string {
	var dirs []string
	home, _ := os.UserHomeDir()
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		dirs = append(dirs, filepath.Join(d, "applications"))
	} else if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local/share/applications"))
	}
	data := os.Getenv("XDG_DATA_DIRS")
	if data == "" {
		data = "/usr/local/share:/usr/share"
	}
	for _, d := range filepath.SplitList(data) {
		dirs = append(dirs, filepath.Join(d, "applications"))
	}
	dirs = append(dirs, "/var/lib/flatpak/exports/share/applications", "/var/lib/snapd/desktop/applications")
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local/share/flatpak/exports/share/applications"))
	}
	return dirs
}

type linuxApp struct {
	appInfo
	exec string
}

func linuxApps() []linuxApp {
	seen := map[string]bool{}
	var apps []linuxApp
	for _, dir := range desktopDirs() {
		files, _ := filepath.Glob(filepath.Join(dir, "*.desktop"))
		for _, f := range files {
			id := filepath.Base(f)
			if seen[id] {
				continue
			}
			seen[id] = true
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			e := parseDesktopEntry(string(data))
			if !e.IsApp || e.Hidden || e.Name == "" {
				continue
			}
			apps = append(apps, linuxApp{appInfo{Name: e.Name, ID: id, Path: f}, execBase(e.Exec)})
		}
	}
	return apps
}

// installedApps lists every visible application .desktop entry; ID is the
// desktop file id, which `gtk-launch` accepts.
func installedApps() ([]appInfo, error) {
	var out []appInfo
	for _, a := range linuxApps() {
		out = append(out, a.appInfo)
	}
	return out, nil
}

// runningApps matches desktop entries to running processes by the program
// their Exec line starts. Linux has no "app" below the desktop, so this is
// the closest honest answer.
func runningApps() ([]appInfo, error) {
	procs, err := listProcesses()
	if err != nil {
		return nil, err
	}
	byName := map[string]int{}
	for _, p := range procs {
		if _, ok := byName[p.Name]; !ok {
			byName[p.Name] = p.PID
		}
	}
	var out []appInfo
	for _, a := range linuxApps() {
		if pid, ok := byName[a.exec]; ok && a.exec != "" {
			a.Running, a.PID = true, pid
			out = append(out, a.appInfo)
		}
	}
	sortApps(out)
	return out, nil
}
