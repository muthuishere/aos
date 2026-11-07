package groups

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// appInfo is one application, in the shape `app list --json` emits.
//
// ID is the platform's stable handle for the app: the bundle id on macOS,
// the desktop file id on Linux (firefox.desktop), and on Windows the
// uninstall-registry key name for installed apps or the executable name for
// running ones. Path is the .app bundle, .desktop file or .exe.
type appInfo struct {
	Name    string `json:"name"`
	ID      string `json:"id,omitempty"`
	Path    string `json:"path,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Running bool   `json:"running"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("app", "Installed and running applications")
		r.Add(&cli.Command{
			Group: "app", Name: "list",
			Summary: "List installed applications, or only the running ones, with id and path",
			Args:    "[--running] [--filter=<substring>] [--json]",
			Examples: []string{
				"aos app list",
				"aos app list --running",
				"aos app list --filter=code --json",
			},
			Run: runAppList,
		})
	})
}

func runAppList(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "filter")
	if err != nil {
		return err
	}
	if err := set.Reject("running", "filter", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`app list` takes no positional arguments; did you mean --filter=%s?", set.Rest[0])
	}
	running, err := runningApps()
	if err != nil {
		return err
	}
	apps := running
	if !set.Has("running") {
		installed, err := installedApps()
		if err != nil {
			return err
		}
		apps = mergeApps(installed, running)
	}
	apps = filterApps(apps, set.String("filter", ""))
	if set.Has("json") {
		if apps == nil {
			apps = []appInfo{}
		}
		return writeJSON(c, apps)
	}
	for _, a := range apps {
		mark := " "
		if a.Running {
			mark = "*"
		}
		c.Printf("%s %-32s %-40s %s\n", mark, a.Name, a.ID, a.Path)
	}
	return nil
}

// mergeApps marks installed apps that are running (matched by id, then path,
// then name) and appends running apps that were not found installed, so a
// portable app started from Downloads still shows. Sorted by name.
func mergeApps(installed, running []appInfo) []appInfo {
	out := append([]appInfo(nil), installed...)
	for _, r := range running {
		hit := -1
		for i, a := range out {
			if (r.ID != "" && strings.EqualFold(a.ID, r.ID)) ||
				samePath(a.Path, r.Path) ||
				strings.EqualFold(a.Name, r.Name) {
				hit = i
				break
			}
		}
		if hit < 0 {
			out = append(out, r)
			continue
		}
		out[hit].Running, out[hit].PID = true, r.PID
	}
	sortApps(out)
	return out
}

func samePath(a, b string) bool {
	return a != "" && b != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func sortApps(apps []appInfo) {
	sort.SliceStable(apps, func(i, j int) bool {
		return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name)
	})
}

// filterApps keeps apps whose name or id contains query, ignoring case.
func filterApps(apps []appInfo, query string) []appInfo {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return apps
	}
	var out []appInfo
	for _, a := range apps {
		if strings.Contains(strings.ToLower(a.Name), query) || strings.Contains(strings.ToLower(a.ID), query) {
			out = append(out, a)
		}
	}
	return out
}

// desktopEntry is the part of a freedesktop .desktop file app list needs.
type desktopEntry struct {
	Name, Exec string
	Hidden     bool // NoDisplay or Hidden: not shown in menus
	IsApp      bool
}

// parseDesktopEntry reads the [Desktop Entry] group, ignoring localized keys
// and every later group (actions).
func parseDesktopEntry(text string) desktopEntry {
	var e desktopEntry
	in := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			in = line == "[Desktop Entry]"
			continue
		}
		if !in {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "Name":
			e.Name = value
		case "Exec":
			e.Exec = value
		case "Type":
			e.IsApp = value == "Application"
		case "NoDisplay", "Hidden":
			if value == "true" {
				e.Hidden = true
			}
		}
	}
	return e
}

// execBase is the program name an Exec line runs: `env FOO=1 /usr/bin/code
// --flag %F` -> code.
func execBase(exec string) string {
	for _, f := range strings.Fields(exec) {
		f = strings.Trim(f, `"`)
		if f == "env" || strings.Contains(f, "=") {
			continue
		}
		return filepath.Base(f)
	}
	return ""
}

// uninstallPath picks an app's path from a Windows Uninstall key: the exe in
// DisplayIcon (`"C:\x\app.exe",0`), else the InstallLocation folder.
func uninstallPath(icon, loc string) string {
	if i := strings.LastIndex(icon, ","); i > 0 {
		icon = icon[:i]
	}
	icon = strings.Trim(icon, `"`)
	if strings.HasSuffix(strings.ToLower(icon), ".exe") {
		return icon
	}
	return loc
}
