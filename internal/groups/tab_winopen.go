package groups

import (
	"fmt"
	"strings"
	"unicode"
)

// Building the Windows `tab open` command lines. Kept free of build tags so
// the argument handling is tested on every platform.
//
// Nothing here goes through cmd.exe: a URL or folder handed to `cmd /c` is
// parsed by cmd, and `x" & calc & "` runs calc. Browsers are started by their
// full exe path (from App Paths) with the URL as one argv entry; Windows
// Terminal is started as wt.exe with argv. Only --command is a shell command,
// by design, and it is documented as one.

// checkTabURL refuses what a browser would read as something other than a
// page: control characters (a newline splits a command line) and a leading
// "-" (Chromium takes it as a switch, e.g. --gpu-launcher).
func checkTabURL(u string) error {
	for _, r := range u {
		if unicode.IsControl(r) {
			return fmt.Errorf("the URL contains a control character (%U); refusing it", r)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(u), "-") {
		return fmt.Errorf("the URL %q starts with '-', which a browser would read as a switch; refusing it", u)
	}
	return nil
}

// windowsBrowserExe names the exe a browser registers under App Paths.
func windowsBrowserExe(app string) (string, bool) {
	a := strings.ToLower(strings.TrimSuffix(app, ".exe"))
	switch {
	case strings.Contains(a, "edge"):
		return "msedge.exe", true
	case strings.Contains(a, "chrome"):
		return "chrome.exe", true
	case strings.Contains(a, "brave"):
		return "brave.exe", true
	}
	return "", false
}

func isWindowsTerminal(app string) bool {
	a := strings.ToLower(strings.TrimSuffix(app, ".exe"))
	return a == "wt" || strings.Contains(a, "windowsterminal") || strings.Contains(a, "windows terminal")
}

// wtEscape escapes ';', which wt.exe treats as "next subcommand" even inside
// one argv entry, so a folder or command cannot add wt subcommands.
func wtEscape(s string) string { return strings.ReplaceAll(s, ";", `\;`) }

// windowsOpenCommand returns the program and argv that open the tab.
// browserPath resolves a browser exe name to its full path.
func windowsOpenCommand(req tabOpenRequest, browserPath func(exe string) (string, error)) (string, []string, error) {
	if err := checkTabURL(req.URL); err != nil {
		return "", nil, err
	}
	if exe, ok := windowsBrowserExe(req.App); ok {
		if req.CWD != "" || req.Command != "" {
			return "", nil, fmt.Errorf("--cwd and --command are for terminals, not %s", req.App)
		}
		path, err := browserPath(exe)
		if err != nil {
			return "", nil, err
		}
		var args []string
		if req.NewWindow {
			args = append(args, "--new-window")
		}
		if req.URL != "" {
			// "--" ends the switches: whatever follows is a URL.
			args = append(args, "--", req.URL)
		}
		return path, args, nil
	}
	if isWindowsTerminal(req.App) {
		if req.URL != "" {
			return "", nil, fmt.Errorf("Windows Terminal opens folders, not URLs; use --cwd")
		}
		for _, v := range []string{req.CWD, req.Command} {
			for _, r := range v {
				if r == '\n' || r == '\r' || r == 0 {
					return "", nil, fmt.Errorf("--cwd and --command must be one line")
				}
			}
		}
		args := []string{"-w", "0"}
		if req.NewWindow {
			args = []string{"-w", "new"}
		}
		args = append(args, "new-tab")
		if req.CWD != "" {
			args = append(args, "-d", wtEscape(req.CWD))
		}
		if req.Command != "" {
			// --command is a shell command by design: cmd /k runs it and
			// keeps the tab open.
			args = append(args, "cmd", "/k", wtEscape(req.Command))
		}
		return "wt.exe", args, nil
	}
	return "", nil, errTabUnsupported{fmt.Sprintf("aos opens tabs on Windows in Edge, Chrome, Brave and Windows Terminal, not %q", req.App)}
}

// windowsCloseChord is the close-one-tab shortcut of an app on Windows. ""
// means aos does not know it, and close is refused rather than guessed: in
// a terminal, ctrl+w reaches the shell instead of closing the tab.
func windowsCloseChord(app string) string {
	a := strings.ToLower(strings.TrimSuffix(app, ".exe"))
	switch {
	case isWindowsTerminal(a):
		return "ctrl+shift+w"
	case strings.Contains(a, "edge"), strings.Contains(a, "chrome"), strings.Contains(a, "brave"),
		strings.Contains(a, "firefox"), strings.Contains(a, "notepad"):
		return "ctrl+w"
	}
	return ""
}

// macCloseChord: cmd+w closes the front tab in every macOS app with tabs;
// apps that do not usually have tabs are refused.
func macCloseChord(app string) string {
	if windowKind(app) == "other" {
		return ""
	}
	return "cmd+w"
}
