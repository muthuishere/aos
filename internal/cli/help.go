package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Common lists the handful of routes shown on the front page, in order.
var Common = []string{
	"update",
	"theme list",
	"theme set",
	"font list",
	"capture screenshot",
	"debug",
}

// PrintRootHelp renders the front page: usage, common commands, all groups.
func PrintRootHelp(w io.Writer, r *Registry) {
	fmt.Fprintln(w, "aos command center")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  aos <command> [args...]")
	fmt.Fprintln(w, "  aos commands [--all] [--json] [--check]")
	fmt.Fprintln(w, "  aos <group> --help")
	fmt.Fprintln(w, "  aos <group> <command> --help")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Screen commands run on the host, the machine whose screen you see")
	fmt.Fprintln(w, "(Windows from WSL, your Mac over SSH). --host=local|windows-host|<socket>")
	fmt.Fprintln(w, "or AOS_HOST overrides it; `aos doctor` shows where they go.")
	fmt.Fprintln(w)

	var common []*Command
	for _, route := range Common {
		group, name, _ := strings.Cut(route, " ")
		if cmd := r.Lookup(group, name); cmd != nil && !cmd.Hidden {
			common = append(common, cmd)
		}
	}
	if len(common) > 0 {
		fmt.Fprintln(w, "Common commands:")
		width := 0
		for _, cmd := range common {
			if n := len(cmd.Route()); n > width {
				width = n
			}
		}
		for _, cmd := range common {
			fmt.Fprintf(w, "  aos %-*s  %s\n", width, cmd.Route(), cmd.Summary)
		}
		fmt.Fprintln(w)
	}

	groups := visibleGroups(r)
	if len(groups) > 0 {
		fmt.Fprintln(w, "Groups:")
		width := 0
		for _, g := range groups {
			if len(g.Name) > width {
				width = len(g.Name)
			}
		}
		for _, g := range groups {
			fmt.Fprintf(w, "  %-*s  %s\n", width, g.Name, g.Description)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Run `aos commands --all` to list every command, including")
	fmt.Fprintln(w, "the ones this platform cannot run.")
}

// PrintGroupHelp renders one group's commands. display says whether this
// machine has a display server, so GUI commands can be flagged as unavailable
// rather than silently listed as though they would work.
func PrintGroupHelp(w io.Writer, g *Group, goos string, display bool) {
	header := g.Name
	if g.Description != "" {
		header += " — " + g.Description
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w)

	cmds := visibleCommands(g)
	if len(cmds) == 0 {
		fmt.Fprintf(w, "No commands in this group.\n")
		return
	}
	fmt.Fprintln(w, "Commands:")
	width := 0
	for _, cmd := range cmds {
		if n := len(usageLine(cmd)); n > width {
			width = n
		}
	}
	for _, cmd := range cmds {
		suffix := ""
		switch {
		case !cmd.Supports(goos):
			suffix = fmt.Sprintf("  (not on %s)", goos)
		case cmd.NeedsDisplay && !display:
			suffix = "  (needs a display)"
		}
		fmt.Fprintf(w, "  %-*s  %s%s\n", width, usageLine(cmd), cmd.Summary, suffix)
	}
}

// PrintCommandHelp renders one command's own page.
func PrintCommandHelp(w io.Writer, cmd *Command, goos string, display bool) {
	fmt.Fprintf(w, "aos %s — %s\n", cmd.Route(), cmd.Summary)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintf(w, "  %s\n", usageLine(cmd))
	if !cmd.Supports(goos) {
		fmt.Fprintln(w)
		if len(cmd.Platforms) == 0 {
			fmt.Fprintf(w, "Not supported on %s.\n", goos)
		} else {
			fmt.Fprintf(w, "Not supported on %s. Available on: %s.\n", goos, strings.Join(cmd.Platforms, ", "))
		}
	}
	if cmd.NeedsDisplay {
		fmt.Fprintln(w)
		if display {
			fmt.Fprintln(w, "Needs a display; this machine has one.")
		} else {
			fmt.Fprintln(w, "Needs a display, and this machine has none.")
			fmt.Fprintln(w, "Start one with `aos headless start`.")
		}
	}
	if len(cmd.Aliases) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "Aliases: %s\n", strings.Join(cmd.Aliases, ", "))
	}
	if len(cmd.Examples) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Examples:")
		for _, ex := range cmd.Examples {
			fmt.Fprintf(w, "  %s\n", ex)
		}
	}
}

func usageLine(cmd *Command) string {
	line := "aos " + cmd.Route()
	if cmd.Args != "" {
		line += " " + cmd.Args
	}
	return line
}

func visibleGroups(r *Registry) []*Group {
	var out []*Group
	for _, g := range r.Groups() {
		if len(visibleCommands(g)) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func visibleCommands(g *Group) []*Command {
	var out []*Command
	for _, cmd := range g.Commands {
		if !cmd.Hidden {
			out = append(out, cmd)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
