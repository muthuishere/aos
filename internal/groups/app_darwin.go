package groups

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/muthuishere/aos/internal/sys"
)

// runningAppsJXA asks NSWorkspace for the regular (Dock-visible) apps: one
// call, no Accessibility permission needed.
const runningAppsJXA = `ObjC.import("AppKit");
var a=$.NSWorkspace.sharedWorkspace.runningApplications, o=[];
for (var i=0;i<a.count;i++){var x=a.objectAtIndex(i); if (x.activationPolicy!=0) continue;
o.push({name:ObjC.unwrap(x.localizedName)||"", id:ObjC.unwrap(x.bundleIdentifier)||"",
path:x.bundleURL.isNil()?"":ObjC.unwrap(x.bundleURL.path), pid:x.processIdentifier, running:true});}
JSON.stringify(o)`

func runningApps() ([]appInfo, error) {
	out, err := sys.Output("osascript", "-l", "JavaScript", "-e", runningAppsJXA)
	if err != nil {
		return nil, err
	}
	var apps []appInfo
	if err := json.Unmarshal([]byte(out), &apps); err != nil {
		return nil, err
	}
	sortApps(apps)
	return apps, nil
}

// installedApps scans the folders Launchpad shows, one subfolder deep
// (Utilities, vendor folders).
func installedApps() ([]appInfo, error) {
	dirs := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	seen := map[string]bool{}
	var apps []appInfo
	add := func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		a := appInfo{Name: strings.TrimSuffix(filepath.Base(path), ".app"), Path: path}
		if id, err := sys.Output("plutil", "-extract", "CFBundleIdentifier", "raw", filepath.Join(path, "Contents", "Info.plist")); err == nil {
			a.ID = strings.TrimSpace(id)
		}
		apps = append(apps, a)
	}
	for _, dir := range dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if strings.HasSuffix(e.Name(), ".app") {
				add(p)
				continue
			}
			if !e.IsDir() {
				continue
			}
			sub, _ := os.ReadDir(p)
			for _, s := range sub {
				if strings.HasSuffix(s.Name(), ".app") {
					add(filepath.Join(p, s.Name()))
				}
			}
		}
	}
	return apps, nil
}
