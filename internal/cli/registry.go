package cli

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// Runner executes a command. args excludes the route words.
type Runner func(c *Ctx, args []string) error

// Command is one leaf of the CLI tree.
//
// A Command with an empty Name is the group's default command, invoked as
// `aos <group>` with no subcommand (mirrors omarchy's `omarchy-<group>`
// binary sitting alongside its `omarchy-<group>-<name>` siblings).
type Command struct {
	Group    string
	Name     string
	Summary  string
	Args     string
	Examples []string
	Aliases  []string // extra top-level routes, e.g. "screenshot"
	Hidden   bool
	// Blocking marks a command that runs until interrupted. It is fine at a
	// terminal but has no sensible end for a request/response caller, so the
	// MCP surface leaves it out.
	Blocking bool
	// NeedsDisplay marks a command that drives the GUI — windows, input,
	// screenshots. On a headless machine it is refused with an explanation
	// instead of failing deep inside a display-server call.
	NeedsDisplay bool
	// FollowsHost marks a screen command: it runs on the host, the machine
	// whose screen the user sees (ADR 0001), which may not be this one. Add
	// sets it from hostTable; a command may also set it itself.
	FollowsHost bool
	Platforms   []string // nil means every platform; else darwin/windows/linux
	Run         Runner

	// Binary is set for commands discovered from an external executable
	// instead of compiled in. Run is nil in that case.
	Binary string
}

// Route is the space-separated path a user types after the program name.
func (c *Command) Route() string {
	if c.Name == "" {
		return c.Group
	}
	return c.Group + " " + c.Name
}

// Supports reports whether the command can run on goos.
func (c *Command) Supports(goos string) bool {
	if len(c.Platforms) == 0 {
		return true
	}
	for _, p := range c.Platforms {
		if p == goos {
			return true
		}
	}
	return false
}

// Available reports whether the command can run on the current machine.
func (c *Command) Available() bool { return c.Supports(runtime.GOOS) }

// Group is a named bucket of related commands.
type Group struct {
	Name        string
	Description string
	Commands    []*Command
}

// Registry is the whole command tree.
type Registry struct {
	groups   map[string]*Group
	order    []string
	routes   map[string]*Command
	aliases  map[string]*Command
	warnings []string
}

func NewRegistry() *Registry {
	return &Registry{
		groups:  map[string]*Group{},
		routes:  map[string]*Command{},
		aliases: map[string]*Command{},
	}
}

// Group returns the named group, or nil.
func (r *Registry) Group(name string) *Group { return r.groups[name] }

// Groups returns every group, alphabetically.
func (r *Registry) Groups() []*Group {
	out := make([]*Group, 0, len(r.groups))
	for _, name := range r.order {
		out = append(out, r.groups[name])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Commands returns every command, sorted by route.
func (r *Registry) Commands() []*Command {
	var out []*Command
	for _, g := range r.groups {
		out = append(out, g.Commands...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route() < out[j].Route() })
	return out
}

// Warnings collects non-fatal registration problems, surfaced by `commands --check`.
func (r *Registry) Warnings() []string { return r.warnings }

// Describe registers a group description. Safe to call before or after Add.
func (r *Registry) Describe(name, description string) {
	g := r.ensure(name)
	g.Description = description
}

func (r *Registry) ensure(name string) *Group {
	if g, ok := r.groups[name]; ok {
		return g
	}
	g := &Group{Name: name}
	r.groups[name] = g
	r.order = append(r.order, name)
	return g
}

// Add registers commands, rejecting duplicate routes.
func (r *Registry) Add(cmds ...*Command) {
	for _, cmd := range cmds {
		if cmd.Group == "" {
			r.warnings = append(r.warnings, fmt.Sprintf("command %q has no group", cmd.Name))
			continue
		}
		g := r.ensure(cmd.Group)
		if existing := r.Lookup(cmd.Group, cmd.Name); existing != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("duplicate route %q", cmd.Route()))
			continue
		}
		if follows, _ := HostSide(cmd.Route()); follows && cmd.Binary == "" {
			cmd.FollowsHost = true
		}
		g.Commands = append(g.Commands, cmd)
		r.routes[cmd.Route()] = cmd
		for _, alias := range cmd.Aliases {
			if prev, clash := r.aliases[alias]; clash {
				r.warnings = append(r.warnings,
					fmt.Sprintf("alias %q claimed by both %q and %q", alias, prev.Route(), cmd.Route()))
				continue
			}
			r.aliases[alias] = cmd
		}
	}
}

// Lookup finds a command by group and name ("" for the group default).
func (r *Registry) Lookup(group, name string) *Command {
	if name == "" {
		return r.routes[group]
	}
	return r.routes[group+" "+name]
}

// Alias resolves a top-level shortcut such as `screenshot`.
func (r *Registry) Alias(name string) *Command { return r.aliases[name] }

// Resolve maps user arguments onto a command, returning the remaining args.
//
// Routes are multi-token (`audio input set default`), so resolution takes the
// longest matching prefix of args, then falls back to the group default and
// finally to a top-level alias.
func (r *Registry) Resolve(args []string) (*Command, []string, error) {
	if len(args) == 0 {
		return nil, nil, nil
	}
	for n := len(args); n >= 1; n-- {
		if cmd, ok := r.routes[strings.Join(args[:n], " ")]; ok {
			return cmd, args[n:], nil
		}
	}
	if cmd := r.Alias(args[0]); cmd != nil {
		return cmd, args[1:], nil
	}
	if g := r.groups[args[0]]; g != nil {
		// A bare group name, or a group name followed by a help flag, both mean
		// "document this group". Only the first was handled, so `aos
		// window --help` reported the help flag as an unknown command and then
		// suggested the very command that had just failed.
		if len(args) == 1 || (len(args) == 2 && isHelpFlag(args[1])) {
			return nil, nil, &GroupHelpError{Group: g}
		}
		return nil, nil, fmt.Errorf("unknown command %q in group %q\nTry: aos %s --help",
			strings.Join(args[1:], " "), args[0], args[0])
	}
	return nil, nil, fmt.Errorf("unknown command %q\nTry: aos --help", strings.Join(args, " "))
}

// GroupHelpError signals that the user typed a bare group name and wants its help.
type GroupHelpError struct{ Group *Group }

func (e *GroupHelpError) Error() string { return "group help: " + e.Group.Name }
