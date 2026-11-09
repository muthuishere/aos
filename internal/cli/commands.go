package cli

import (
	"encoding/json"
	"fmt"
)

type commandJSON struct {
	Route     string   `json:"route"`
	Group     string   `json:"group"`
	Name      string   `json:"name"`
	Summary   string   `json:"summary"`
	Args      string   `json:"args,omitempty"`
	Examples  []string `json:"examples,omitempty"`
	Aliases   []string `json:"aliases,omitempty"`
	Platforms []string `json:"platforms,omitempty"`
	Available bool     `json:"available"`
	NeedsGUI  bool     `json:"needs_display"`
	Hidden    bool     `json:"hidden"`
	Source    string   `json:"source"`
	Binary    string   `json:"binary,omitempty"`
	// Host is where the command runs under WSL or SSH: "windows-host" follows
	// the person's screen (aos.exe from WSL, the forwarded socket over SSH),
	// "local" always runs where aos was started.
	Host string `json:"host"`
}

// runCommands implements `aos commands [--all] [--json] [--check]`.
func runCommands(c *Ctx, args []string) error {
	var all, asJSON, check bool
	for _, arg := range args {
		switch arg {
		case "--all", "-a":
			all = true
		case "--json":
			asJSON = true
		case "--check":
			check = true
		default:
			return fmt.Errorf("unknown flag %q for `commands`", arg)
		}
	}

	if check {
		return runCommandsCheck(c, asJSON)
	}

	display := HasDisplay(c.Env, c.GOOS)
	var selected []*Command
	for _, cmd := range c.Registry.Commands() {
		if !all && (cmd.Hidden || !cmd.Supports(c.GOOS) || (cmd.NeedsDisplay && !display)) {
			continue
		}
		selected = append(selected, cmd)
	}

	if asJSON {
		payload := struct {
			OK       bool          `json:"ok"`
			Platform string        `json:"platform"`
			Commands []commandJSON `json:"commands"`
		}{OK: true, Platform: c.GOOS, Commands: make([]commandJSON, 0, len(selected))}
		for _, cmd := range selected {
			payload.Commands = append(payload.Commands, toJSON(cmd, c.GOOS))
		}
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}

	width := 0
	for _, cmd := range selected {
		if n := len(cmd.Route()); n > width {
			width = n
		}
	}
	for _, cmd := range selected {
		// "-" is unavailable on this platform, "g" is available but waiting on
		// a display. A second column "^" means that under WSL or SSH the
		// command runs on the host, the machine with the screen.
		mark := " "
		switch {
		case !cmd.Supports(c.GOOS):
			mark = "-"
		case cmd.NeedsDisplay && !display:
			mark = "g"
		}
		side := " "
		if cmd.FollowsHost {
			side = "^"
		}
		c.Printf("%s%s %-*s  %s\n", mark, side, width, cmd.Route(), cmd.Summary)
	}
	return nil
}

func runCommandsCheck(c *Ctx, asJSON bool) error {
	problems := append([]string(nil), c.Registry.Warnings()...)
	for _, cmd := range c.Registry.Commands() {
		if cmd.Summary == "" {
			problems = append(problems, fmt.Sprintf("%s: missing summary", cmd.Route()))
		}
		// A command with no example is not self-explanatory, and this surface is
		// read by agents as much as by people. Enforcing it here is what keeps
		// it true as commands are added.
		if len(cmd.Examples) == 0 && cmd.Binary == "" {
			problems = append(problems, fmt.Sprintf("%s: missing an example", cmd.Route()))
		}
		if cmd.Run == nil && cmd.Binary == "" {
			problems = append(problems, fmt.Sprintf("%s: no implementation", cmd.Route()))
		}
		for _, p := range cmd.Platforms {
			switch p {
			case "darwin", "windows", "linux":
			default:
				problems = append(problems, fmt.Sprintf("%s: unknown platform %q", cmd.Route(), p))
			}
		}
	}
	for _, g := range c.Registry.Groups() {
		if g.Description == "" {
			problems = append(problems, fmt.Sprintf("group %s: missing description", g.Name))
		}
	}

	if asJSON {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			OK       bool     `json:"ok"`
			Problems []string `json:"problems"`
		}{OK: len(problems) == 0, Problems: problems}); err != nil {
			return err
		}
	} else if len(problems) == 0 {
		c.Printf("ok: %d commands across %d groups\n",
			len(c.Registry.Commands()), len(c.Registry.Groups()))
	} else {
		for _, p := range problems {
			c.Printf("%s\n", p)
		}
	}
	if len(problems) > 0 {
		return &ExitError{Code: 1}
	}
	return nil
}

func toJSON(cmd *Command, goos string) commandJSON {
	source := "builtin"
	if cmd.Binary != "" {
		source = "external"
	}
	return commandJSON{
		Route:     "aos " + cmd.Route(),
		Group:     cmd.Group,
		Name:      cmd.Name,
		Summary:   cmd.Summary,
		Args:      cmd.Args,
		Examples:  cmd.Examples,
		Aliases:   cmd.Aliases,
		Platforms: cmd.Platforms,
		Available: cmd.Supports(goos),
		NeedsGUI:  cmd.NeedsDisplay,
		Hidden:    cmd.Hidden,
		Source:    source,
		Binary:    cmd.Binary,
		Host:      HostOf(cmd),
	}
}
