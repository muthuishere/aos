package groups

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// Windows Terminal tabs and their shells.
//
// UI Automation lists each window's tabs (TabItem, in order) but not which
// process is behind a tab. Windows Terminal runs every window in one
// WindowsTerminal.exe, which starts one OpenConsole.exe per pane, and the
// shell runs under that OpenConsole. So a tab is matched to a shell by:
//
//  1. title: the shell's console title (read by attaching to its console)
//     equals the tab's title, and no other unmatched shell has it. A tab
//     opened with --title keeps that title only when the shell sets it too
//     (aos tab open does: `cmd /k title <t>`).
//  2. order: tabs left over, window by window and left to right, take the
//     shells left over in creation order. Tabs are created in that order
//     unless they were dragged between windows, so this is a best guess
//     and is reported as match=order.
//
// A tab with several panes gets the shell of its first pane.

// shellsFor is the engine call, a seam for tests.
var shellsFor = engine.TerminalShells

func annotateTerminalTabs(tabs []Tab, windows []engine.Window) []Tab {
	byID := map[string]engine.Window{}
	for _, w := range windows {
		byID[w.ID] = w
	}
	// Group the WT tabs by terminal process (one process serves many windows).
	type group struct {
		shells []engine.ShellInfo
		tabs   []int
	}
	groups := map[int]*group{}
	var order []int
	for i, t := range tabs {
		w, ok := byID[t.Window]
		if !ok || !isWindowsTerminal(t.App) {
			continue
		}
		g, ok := groups[w.PID]
		if !ok {
			shells, err := shellsFor(w)
			if err != nil {
				continue
			}
			g = &group{shells: shells}
			groups[w.PID] = g
			order = append(order, w.PID)
		}
		g.tabs = append(g.tabs, i)
	}
	for _, pid := range order {
		g := groups[pid]
		matchShells(tabs, g.tabs, g.shells)
	}
	return tabs
}

// matchShells assigns shells to the tabs at idx (title first, then order).
func matchShells(tabs []Tab, idx []int, shells []engine.ShellInfo) {
	used := make([]bool, len(shells))
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	for _, i := range idx {
		hit, n := -1, 0
		for j, s := range shells {
			if !used[j] && norm(s.Title) != "" && norm(s.Title) == norm(tabs[i].Title) {
				hit, n = j, n+1
			}
		}
		if n == 1 {
			used[hit] = true
			tabs[i].PID, tabs[i].CWD, tabs[i].Match = shells[hit].PID, shells[hit].CWD, "title"
		}
	}
	rest := append([]int(nil), idx...)
	sort.SliceStable(rest, func(a, b int) bool {
		ta, tb := tabs[rest[a]], tabs[rest[b]]
		if ta.Window != tb.Window {
			return false // keep window order as listed
		}
		return ta.Index < tb.Index
	})
	j := 0
	for _, i := range rest {
		if tabs[i].PID != 0 {
			continue
		}
		for j < len(shells) && used[j] {
			j++
		}
		if j == len(shells) {
			return
		}
		used[j] = true
		tabs[i].PID, tabs[i].CWD, tabs[i].Match = shells[j].PID, shells[j].CWD, "order"
	}
}

// terminalTab resolves the tab flags to a terminal tab with a shell, for
// `tab read`, `tab type` and `key type --tab`. ok=false: not a terminal tab.
func terminalTab(f tabFilter) (Tab, bool, error) {
	if f.Kind == "" {
		f.Kind = "terminal"
	}
	if f.Kind != "terminal" {
		return Tab{}, false, nil
	}
	t, err := pickTab(f, false)
	if err != nil || t.PID == 0 {
		return Tab{}, false, err
	}
	return t, true, nil
}

// terminalFilter is the tab selection of `tab read/type` aimed at a
// terminal: no --url (that is a browser), and the tab named some way.
func terminalFilter(set *argSet) (tabFilter, bool) {
	if set.String("url", "") != "" || set.String("element", "") != "" || set.String("into", "") != "" {
		return tabFilter{}, false
	}
	f, err := tabFilterFrom(set)
	if err != nil {
		return tabFilter{}, false
	}
	f.Kind = "terminal"
	if f.Title == "" && f.ID == "" && f.Index == 0 {
		return tabFilter{}, false
	}
	if f.ID != "" && !strings.HasPrefix(f.ID, "uia:") {
		return tabFilter{}, false
	}
	return f, true
}

// terminalRead: `tab read` of a Windows Terminal / console tab prints the
// screen of the tab's shell (its console buffer), whether or not the tab is
// selected or its window is in front. done=false: not a terminal tab.
func terminalRead(c *cli.Ctx, set *argSet) (bool, error) {
	f, ok := terminalFilter(set)
	if !ok {
		return false, nil
	}
	t, ok, _ := terminalTab(f)
	if !ok {
		return false, nil
	}
	screen, err := engine.ConsoleRead(t.PID)
	if err != nil {
		return true, err
	}
	if set.Has("json") {
		return true, json.NewEncoder(c.Stdout).Encode(map[string]any{"tab": t.ID, "title": t.Title, "pid": t.PID, "cwd": t.CWD, "text": screen})
	}
	c.Println(screen)
	return true, nil
}

// terminalType: `tab type --text=... [--enter]` into a terminal tab's shell.
func terminalType(c *cli.Ctx, set *argSet) (bool, error) {
	f, ok := terminalFilter(set)
	if !ok {
		return false, nil
	}
	t, ok, _ := terminalTab(f)
	if !ok {
		return false, nil
	}
	text := set.String("text", "")
	if text == "" && !set.Has("enter") {
		return true, fmt.Errorf("--text is required")
	}
	if text != "" {
		if err := engine.ConsoleType(t.PID, text); err != nil {
			return true, err
		}
	}
	if set.Has("enter") {
		if err := engine.ConsoleKey(t.PID, "enter"); err != nil {
			return true, err
		}
	}
	c.Printf("typed into %s  %s (shell pid %d, console input)\n", t.ID, t.Title, t.PID)
	return true, nil
}
