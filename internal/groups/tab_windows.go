//go:build windows

package groups

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Windows: every app's tabs come from UI Automation (Edge, Chrome, Windows
// Terminal, Notepad all expose TabItem elements). Opening goes through each
// app's own command line, then the new tab is found by listing again.
func init() {
	tabProviders = func() []tabProvider {
		ax := newAXTabs("uia", "ctrl+w", nil)
		ax.opener = func(ctx context.Context, req tabOpenRequest) (string, error) {
			return openWindowsTab(ctx, ax, req)
		}
		return []tabProvider{ax}
	}
}

// windowsOpenArgs builds the command line that opens a tab: the browser's
// own exe for a URL, `wt` for Windows Terminal.
func windowsOpenArgs(req tabOpenRequest) (string, []string, error) {
	app := strings.ToLower(strings.TrimSuffix(req.App, ".exe"))
	switch {
	case strings.Contains(app, "edge"):
		return browserArgs("msedge", req), nil, nil
	case strings.Contains(app, "chrome"):
		return browserArgs("chrome", req), nil, nil
	case strings.Contains(app, "brave"):
		return browserArgs("brave", req), nil, nil
	case strings.Contains(app, "terminal") || app == "wt":
		args := []string{"-w", "0"}
		if req.NewWindow {
			args = []string{"-w", "new"}
		}
		args = append(args, "new-tab")
		if req.CWD != "" {
			args = append(args, "-d", req.CWD)
		}
		if req.Command != "" {
			args = append(args, "cmd", "/k", req.Command)
		}
		return "wt", args, nil
	}
	return "", nil, errTabUnsupported{fmt.Sprintf("aos opens tabs on Windows in Edge, Chrome, Brave and Windows Terminal, not %q", req.App)}
}

// browserArgs uses `cmd /c start` so the exe is found through App Paths,
// where browsers register themselves instead of on PATH.
func browserArgs(exe string, req tabOpenRequest) string {
	parts := []string{`start "" ` + exe}
	if req.NewWindow {
		parts = append(parts, "--new-window")
	}
	if req.URL != "" {
		parts = append(parts, `"`+req.URL+`"`)
	}
	return strings.Join(parts, " ")
}

func openWindowsTab(ctx context.Context, ax *axTabs, req tabOpenRequest) (string, error) {
	if req.Window != "" {
		return "", errTabUnsupported{"--window is not supported on Windows: the browser opens the tab in its last active window"}
	}
	if req.Background {
		return "", errTabUnsupported{"--background is not supported on Windows"}
	}
	before := map[string]bool{}
	for _, t := range appTabs(ax, req.App) {
		before[t.Title] = true
	}
	line, wtArgs, err := windowsOpenArgs(req)
	if err != nil {
		return "", err
	}
	var cmd *exec.Cmd
	if wtArgs != nil {
		cmd = exec.CommandContext(ctx, "wt", wtArgs...)
	} else {
		cmd = exec.CommandContext(ctx, "cmd", "/c", line)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%s: %v %s", req.App, err, strings.TrimSpace(string(out)))
	}
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
