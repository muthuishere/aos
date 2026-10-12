package groups

import (
	"encoding/json"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/skill"
)

func init() {
	register(func(r *cli.Registry) {
		// The only thing this installs is the skill, and `pkg install` sits
		// right beside it -- so a top-level `install` read like it installed
		// anything. It lives with the rest of the skill commands now.
		//
		// The old routes stay as hidden aliases: they are in the v0.1 and v0.2
		// docs and in anything anyone scripted, and a one-line alias is cheaper
		// than a broken command.
		r.Describe("install", "Install the bundled agent skill (use `skill install`)")
		r.Add(&cli.Command{
			Group:    "install",
			Summary:  "Deprecated alias for `skill install`",
			Args:     "[--skills] [--json]",
			Examples: []string{"aos skill install"},
			Hidden:   true,
			Run:      runInstall,
		})

		r.Describe("uninstall", "Remove the installed agent skill (use `skill uninstall`)")
		r.Add(&cli.Command{
			Group:    "uninstall",
			Summary:  "Deprecated alias for `skill uninstall`",
			Args:     "[--skills] [--json]",
			Examples: []string{"aos skill uninstall"},
			Hidden:   true,
			Run:      runUninstall,
		})

		r.Describe("skill", "The agent skill bundled in this binary")
		r.Add(
			&cli.Command{
				Group: "skill", Name: "install",
				Summary:  "Install the bundled agent skill for every agent on this machine",
				Args:     "[--json]",
				Examples: []string{"aos skill install"},
				Run:      runInstall,
			},
			&cli.Command{
				Group: "skill", Name: "uninstall",
				Summary:  "Remove the installed agent skill",
				Args:     "[--json]",
				Examples: []string{"aos skill uninstall"},
				Run:      runUninstall,
			},
			&cli.Command{
				Group: "skill", Name: "show",
				Summary:  "Print the bundled skill without installing it",
				Examples: []string{"aos skill show"},
				Run: func(c *cli.Ctx, _ []string) error {
					content, err := skill.Content()
					if err != nil {
						return err
					}
					c.Printf("%s", content)
					return nil
				},
			},
			&cli.Command{
				Group: "skill", Name: "path",
				Summary:  "Print where `skill install` would write",
				Examples: []string{"aos skill path"},
				Run: func(c *cli.Ctx, _ []string) error {
					hosts, err := skill.Hosts(c.Env)
					if err != nil {
						return err
					}
					for _, host := range hosts {
						c.Printf("%-8s %s/%s\n", host.Name, host.Root, skill.Name)
					}
					return nil
				},
			},
		)
	})
}

func runInstall(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("skills", "json"); err != nil {
		return err
	}
	// `--skills` is still accepted, and ignored: `skill install` already says
	// what it installs, and the flag is in every doc and script written against
	// the old route.

	results, err := skill.Install(c.Env)
	if err != nil {
		return err
	}
	skill.SetAutoInstall(true)
	return reportSkill(c, set, results)
}

func runUninstall(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("skills", "json"); err != nil {
		return err
	}
	results, err := skill.Uninstall(c.Env)
	if err != nil {
		return err
	}
	// Remember the removal, or the next command would put the skill back.
	skill.SetAutoInstall(false)
	return reportSkill(c, set, results)
}

func reportSkill(c *cli.Ctx, set *argSet, results []skill.Result) error {
	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	for _, result := range results {
		c.Printf("%-10s %-8s %s\n", result.Action, result.Host, result.Path)
	}
	return nil
}

// skipsSkillEnsure reports whether argv is one of the commands the automatic
// skill install stays out of: managing the skill itself, and help.
func skipsSkillEnsure(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			return true
		}
	}
	switch args[0] {
	case "install", "uninstall":
		return true
	case "skill":
		return len(args) > 1 && (args[1] == "install" || args[1] == "uninstall")
	}
	return false
}

// EnsureSkill keeps the bundled skill installed and current, so an agent on
// this machine can drive aos without anyone running `skill install`. It says
// one line on stderr when it changed something and never fails the command.
func EnsureSkill(c *cli.Ctx, args []string) {
	if skipsSkillEnsure(args) {
		return
	}
	results, _ := skill.Ensure(c.Env)
	for _, r := range results {
		c.Warnf("aos: %s agent skill at %s (opt out: AOS_NO_SKILL_AUTOINSTALL=1 or `aos skill uninstall`)\n", r.Action, r.Path)
	}
}
