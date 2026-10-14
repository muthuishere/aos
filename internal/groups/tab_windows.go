//go:build windows

package groups

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows/registry"
)

// Windows: every app's tabs come from UI Automation (Edge, Chrome, Windows
// Terminal, Notepad all expose TabItem elements). Opening goes through each
// app's own command line (see tab_winopen.go), then the new tab is found by
// listing again.
func init() {
	tabProviders = func() []tabProvider {
		ax := newAXTabs("uia", windowsCloseChord, nil)
		ax.opener = func(ctx context.Context, req tabOpenRequest) (string, error) {
			return openWindowsTab(ctx, ax, req)
		}
		return []tabProvider{ax}
	}
}

// browserExePath finds a browser through App Paths, where browsers register
// instead of on PATH, then the usual install folders.
func browserExePath(exe string) (string, error) {
	key := `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\` + exe
	for _, root := range []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE} {
		k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		p, _, err := k.GetStringValue("")
		k.Close()
		if p = strings.Trim(p, `"`); err == nil && p != "" {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	sub := map[string]string{
		"msedge.exe": `Microsoft\Edge\Application`,
		"chrome.exe": `Google\Chrome\Application`,
		"brave.exe":  `BraveSoftware\Brave-Browser\Application`,
	}[exe]
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if base := os.Getenv(env); base != "" && sub != "" {
			p := filepath.Join(base, sub, exe)
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("%s is not installed (not in App Paths or Program Files)", exe)
}

func openWindowsTab(ctx context.Context, ax *axTabs, req tabOpenRequest) (string, error) {
	if req.Window != "" {
		return "", errTabUnsupported{"--window is not supported on Windows: the browser opens the tab in its last active window"}
	}
	if req.Background {
		return "", errTabUnsupported{"--background is not supported on Windows"}
	}
	name, args, err := windowsOpenCommand(req, browserExePath)
	if err != nil {
		return "", err
	}
	before := map[string]bool{}
	for _, t := range appTabs(ax, req.App) {
		before[t.Title] = true
	}
	// No context: a browser started here must outlive this command.
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("%s: %v", req.App, err)
	}
	go func() { _ = cmd.Wait() }()
	// The tab appears asynchronously; report the active tab of the app once
	// it shows up, which is the one just opened.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		for _, t := range appTabs(ax, req.App) {
			if t.Active && !before[t.Title] {
				return t.ID, nil
			}
		}
	}
	return "opened (run `aos tab list --app=" + req.App + "` for its id)", nil
}

func appTabs(ax *axTabs, app string) []Tab {
	ws, err := ax.windows()
	if err != nil {
		return nil
	}
	tabs, _ := collectTabs(tabFilter{App: app}, ws)
	return tabs
}
