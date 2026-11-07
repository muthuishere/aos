package groups

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/muthuishere/aos/internal/sys"
)

// Mission Control's own binary takes "1" to toggle Show Desktop, the same
// as the F11 / trackpad gesture, without synthesizing a keystroke.
const missionControl = "/System/Applications/Mission Control.app/Contents/MacOS/Mission Control"

func showDesktop() error {
	return sys.Spawn(missionControl, "1")
}

func hideOthers() (int, error) {
	_, err := sys.Osascript(`tell application "System Events" to set visible of every process whose visible is true and frontmost is false to false`)
	return -1, err
}

const regularAppsJXA = `ObjC.import("AppKit");
var a=$.NSWorkspace.sharedWorkspace.runningApplications, o=[];
for (var i=0;i<a.count;i++){var x=a.objectAtIndex(i); if (x.activationPolicy!=0) continue;
o.push({Name:ObjC.unwrap(x.localizedName)||"", PID:x.processIdentifier});}
JSON.stringify(o)`

func quitCandidates() ([]quitTarget, error) {
	out, err := sys.Output("osascript", "-l", "JavaScript", "-e", regularAppsJXA)
	if err != nil {
		return nil, err
	}
	var apps []quitTarget
	return apps, json.Unmarshal([]byte(out), &apps)
}

// quitApp asks the app to quit the normal way, so it can offer to save.
func quitApp(t quitTarget) error {
	_, err := sys.Osascript(quitScript(t.PID))
	return err
}

// quitScript addresses the app by bundle id, since a process name ("Code")
// is not always the app name ("Visual Studio Code").
func quitScript(pid int) string {
	return fmt.Sprintf(`tell application "System Events" to set b to bundle identifier of first process whose unix id is %s
tell application id b to quit`, strconv.Itoa(pid))
}
