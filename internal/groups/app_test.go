package groups

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

func TestMergeApps(t *testing.T) {
	installed := []appInfo{
		{Name: "Safari", ID: "com.apple.Safari", Path: "/Applications/Safari.app"},
		{Name: "code", ID: "code.desktop", Path: "/usr/share/applications/code.desktop"},
		{Name: "Notes", ID: "com.apple.Notes"},
	}
	running := []appInfo{
		{Name: "Safari", ID: "com.apple.Safari", PID: 10, Running: true},
		{Name: "Tool", Path: "/tmp/Tool.app", PID: 20, Running: true},
		{Name: "CODE", PID: 30, Running: true},
	}
	got := mergeApps(installed, running)
	if len(got) != 4 {
		t.Fatalf("got %+v", got)
	}
	want := map[string]int{"Safari": 10, "code": 30, "Notes": 0, "Tool": 20}
	for _, a := range got {
		if a.PID != want[a.Name] || a.Running != (want[a.Name] != 0) {
			t.Errorf("%s: %+v", a.Name, a)
		}
	}
	if got[0].Name != "code" || got[3].Name != "Tool" {
		t.Errorf("not sorted by name: %+v", got)
	}
}

func TestFilterApps(t *testing.T) {
	apps := []appInfo{{Name: "Visual Studio Code", ID: "com.microsoft.VSCode"}, {Name: "Safari", ID: "com.apple.Safari"}}
	if got := filterApps(apps, "vscode"); len(got) != 1 || got[0].Name != "Visual Studio Code" {
		t.Errorf("by id: %+v", got)
	}
	if got := filterApps(apps, "SAF"); len(got) != 1 {
		t.Errorf("by name: %+v", got)
	}
	if got := filterApps(apps, ""); len(got) != 2 {
		t.Errorf("empty filter: %+v", got)
	}
}

func TestParseDesktopEntry(t *testing.T) {
	e := parseDesktopEntry(`[Desktop Entry]
Name=Firefox
Name[de]=Feuerfuchs
Exec=env MOZ=1 /usr/lib/firefox/firefox %u
Type=Application

[Desktop Action new-window]
Name=New Window
NoDisplay=true
`)
	if e.Name != "Firefox" || !e.IsApp || e.Hidden {
		t.Errorf("got %+v", e)
	}
	if b := execBase(e.Exec); b != "firefox" {
		t.Errorf("execBase = %q", b)
	}
	if !parseDesktopEntry("[Desktop Entry]\nName=x\nType=Application\nNoDisplay=true\n").Hidden {
		t.Error("NoDisplay should hide")
	}
}

func TestUninstallPath(t *testing.T) {
	if got := uninstallPath(`"C:\Program Files\App\app.exe",0`, `C:\Program Files\App`); got != `C:\Program Files\App\app.exe` {
		t.Errorf("icon exe: %q", got)
	}
	if got := uninstallPath(`C:\x\icon.ico`, `C:\x`); got != `C:\x` {
		t.Errorf("fallback: %q", got)
	}
}

func TestAppListRunningLive(t *testing.T) {
	var out bytes.Buffer
	if err := runAppList(&cli.Ctx{Stdout: &out}, []string{"--running", "--json"}); err != nil {
		t.Fatal(err)
	}
	var apps []appInfo
	if err := json.Unmarshal(out.Bytes(), &apps); err != nil {
		t.Fatal(err)
	}
	for _, a := range apps {
		if !a.Running || a.Name == "" {
			t.Errorf("bad running app: %+v", a)
		}
	}
}
