package groups

import (
	"runtime"
	"sort"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// probedTools are the helpers the backends shell out to. `debug` reports which
// ones are present so a missing dependency is one command away from obvious.
var probedTools = map[string][]string{
	"darwin":  {"brew", "screencapture", "pmset", "osascript", "networksetup", "system_profiler"},
	"windows": {"winget", "scoop", "choco", "netsh", "clip.exe"},
	// wmctrl / xdotool / xrandr and a screenshot tool are what the window,
	// input, and capture backends shell out to on X11; Xvfb is what `headless
	// start` needs on a machine with no screen.
	"linux": {
		"pacman", "yay", "apt", "dnf",
		"grim", "slurp", "maim", "scrot", "import",
		"wl-copy", "xclip", "wpctl", "pactl", "nmcli",
		"hyprctl", "xrandr", "wmctrl", "xdotool", "Xvfb", "loginctl",
	},
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("debug", "Diagnostics for bug reports")
		r.Add(
			&cli.Command{
				Group:    "debug",
				Summary:  "Print a diagnostic dump: platform, tools, plugin dirs",
				Examples: []string{"aos debug"},
				Run:      runDebug,
			},
			&cli.Command{
				Group: "debug", Name: "tools",
				Summary:  "Report which helper tools are installed",
				Examples: []string{"aos debug tools"},
				Run:      runDebugTools,
			},
		)

		r.Describe("version", "Version information")
		r.Add(&cli.Command{
			Group:    "version",
			Summary:  "Print the aos version",
			Examples: []string{"aos version"},
			Run: func(c *cli.Ctx, _ []string) error {
				c.Println(c.Version)
				return nil
			},
		})
	})
}

func runDebug(c *cli.Ctx, _ []string) error {
	c.Printf("aos  %s\n", c.Version)
	c.Printf("go          %s\n", runtime.Version())
	c.Printf("platform    %s/%s\n", runtime.GOOS, runtime.GOARCH)

	available := 0
	for _, cmd := range c.Registry.Commands() {
		if cmd.Supports(c.GOOS) {
			available++
		}
	}
	c.Printf("commands    %d available of %d registered, in %d groups\n",
		available, len(c.Registry.Commands()), len(c.Registry.Groups()))

	c.Println()
	c.Println("system:")
	if facts, err := systemInfo(); err == nil {
		keys := make([]string, 0, len(facts))
		for key := range facts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			c.Printf("  %-11s %s\n", key, facts[key])
		}
	}

	c.Println()
	c.Println("plugin dirs:")
	for _, dir := range cli.PluginDirs(c.Env) {
		c.Printf("  %s\n", dir)
	}

	c.Println()
	c.Println("tools:")
	return printTools(c)
}

func runDebugTools(c *cli.Ctx, _ []string) error { return printTools(c) }

func printTools(c *cli.Ctx) error {
	tools := append([]string(nil), probedTools[c.GOOS]...)
	sort.Strings(tools)
	width := 0
	for _, tool := range tools {
		if len(tool) > width {
			width = len(tool)
		}
	}
	for _, tool := range tools {
		mark := "missing"
		if sys.Has(tool) {
			mark = "ok"
		}
		c.Printf("  %-*s  %s\n", width, tool, mark)
	}
	return nil
}
