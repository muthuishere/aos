package groups

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// quitTarget is one running app `app quit-all` may close.
type quitTarget struct {
	Name string
	PID  int
}

// neverQuit are the shell apps quitting would break the desktop with, or
// that refuse to quit anyway.
var neverQuit = map[string]bool{
	"finder": true, "dock": true, "systemuiserver": true, "explorer": true,
	"gnome-shell": true, "plasmashell": true,
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("app", "Installed and running applications")
		r.Add(
			&cli.Command{
				Group: "app", Name: "hide-others",
				Summary:  "Hide (minimize) every app except the frontmost one",
				Examples: []string{"aos app hide-others"},
				Run:      runAppHideOthers,
			},
			&cli.Command{
				Group: "app", Name: "quit-all",
				Summary: "Quit every running app, except those named; asks first unless --yes",
				Args:    "[--except=<name,name>] [--yes]",
				Examples: []string{
					"aos app quit-all                       # lists what would quit, then asks",
					`aos app quit-all --except="Slack,Visual Studio Code" --yes`,
				},
				Run: runAppQuitAll,
			},
			&cli.Command{
				Group: "window", Name: "desktop",
				Summary:  "Show the desktop (toggle: run again to bring the windows back)",
				Examples: []string{"aos window desktop"},
				Run:      func(c *cli.Ctx, _ []string) error { return showDesktop() },
			},
		)
	})
}

func runAppHideOthers(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`app hide-others` takes no arguments")
	}
	n, err := hideOthers()
	if err != nil {
		return err
	}
	if n >= 0 {
		c.Printf("hid %d windows\n", n)
	}
	return nil
}

func runAppQuitAll(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "except")
	if err != nil {
		return err
	}
	if err := set.Reject("except", "yes", "y"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("unknown argument %q; name apps to keep with --except", set.Rest[0])
	}
	running, err := quitCandidates()
	if err != nil {
		return err
	}
	targets := selectQuitTargets(running, splitExcept(set.String("except", "")), ancestorPIDs())
	if len(targets) == 0 {
		c.Println("nothing to quit")
		return nil
	}
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Name
	}
	if !(set.Has("yes") || set.Has("y")) {
		ok, err := confirmQuit(c, names, stdinIsTerminal())
		if err != nil || !ok {
			return err
		}
	}
	failed := 0
	for _, t := range targets {
		if err := quitApp(t); err != nil {
			c.Warnf("aos: could not quit %s: %v\n", t.Name, err)
			failed++
			continue
		}
		c.Printf("quit %s\n", t.Name)
	}
	if failed > 0 {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%d app(s) did not quit", failed)}
	}
	return nil
}

// confirmQuit shows what would quit and asks. Without a terminal to ask on
// it refuses, so an agent has to say --yes on purpose.
func confirmQuit(c *cli.Ctx, names []string, interactive bool) (bool, error) {
	c.Printf("will quit %d app(s): %s\n", len(names), strings.Join(names, ", "))
	if !interactive {
		return false, &cli.ExitError{Code: 1, Message: "not quitting without confirmation; re-run with --yes (unsaved work may be lost)"}
	}
	c.Printf("quit them? [y/N] ")
	in := c.Stdin
	if in == nil {
		in = os.Stdin
	}
	return readYes(in), nil
}

func readYes(r io.Reader) bool {
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func splitExcept(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, strings.ToLower(f))
		}
	}
	return out
}

// selectQuitTargets drops the desktop shell, every app named in except
// (case-insensitive, exact or substring), and anything in aos's own
// ancestry, so the terminal running the agent never quits under it.
// One entry per app name, sorted.
func selectQuitTargets(running []quitTarget, except []string, ancestors map[int]bool) []quitTarget {
	seen := map[string]bool{}
	var out []quitTarget
	for _, t := range running {
		lower := strings.ToLower(strings.TrimSuffix(t.Name, ".exe"))
		if t.Name == "" || neverQuit[lower] || ancestors[t.PID] || seen[lower] {
			continue
		}
		skip := false
		for _, e := range except {
			if strings.Contains(lower, e) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		seen[lower] = true
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// windowOwners is one quit target per pid that owns a window.
func windowOwners(wins []engine.Window) []quitTarget {
	seen := map[int]bool{}
	var out []quitTarget
	for _, w := range wins {
		if w.PID <= 0 || seen[w.PID] {
			continue
		}
		seen[w.PID] = true
		out = append(out, quitTarget{Name: w.App, PID: w.PID})
	}
	return out
}

// othersThanFront is every window not owned by the focused window's
// process. With no focused window it is every window.
func othersThanFront(wins []engine.Window) []engine.Window {
	front := -1
	for _, w := range wins {
		if w.Focused {
			front = w.PID
		}
	}
	var out []engine.Window
	for _, w := range wins {
		if w.PID != front {
			out = append(out, w)
		}
	}
	return out
}

// desktopToggle reads Linux `wmctrl -m` ("showing the desktop: ON") and
// returns the state to switch to.
func desktopToggle(wmctrlM string) string {
	if strings.Contains(strings.ToUpper(wmctrlM), "SHOWING THE DESKTOP: ON") {
		return "off"
	}
	return "on"
}

// ancestorChain walks parent links from pid up to the root.
func ancestorChain(pid int, parent map[int]int) map[int]bool {
	out := map[int]bool{}
	for pid > 1 && !out[pid] {
		out[pid] = true
		pid = parent[pid]
	}
	return out
}

// parsePidPpid reads `ps -axo pid=,ppid=`.
func parsePidPpid(text string) map[int]int {
	out := map[int]int{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			out[pid] = ppid
		}
	}
	return out
}
