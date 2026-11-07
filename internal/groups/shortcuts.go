package groups

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// shortcutInfo is one Shortcuts automation, in `shortcuts list --json` shape.
type shortcutInfo struct {
	Name string `json:"name"`
	ID   string `json:"id,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("shortcuts", "Run the person's Shortcuts automations (macOS)")
		mac := []string{"darwin"}
		r.Add(
			&cli.Command{
				Group: "shortcuts", Name: "list",
				Summary: "List the Shortcuts automations, optionally in one folder",
				Args:    "[--folder=<name>] [--json]",
				Examples: []string{
					"aos shortcuts list",
					"aos shortcuts list --folder=Work --json",
				},
				Platforms: mac,
				Run:       runShortcutsList,
			},
			&cli.Command{
				Group: "shortcuts", Name: "run",
				Summary: "Run a shortcut by name or id, with optional input, and print its output",
				Args:    "<name|id> [--input=<file>] [--text=<input text>]",
				Examples: []string{
					`aos shortcuts run "Resize Image" --input=~/Desktop/photo.jpg`,
					`aos shortcuts run "Translate" --text="good morning"`,
					`aos shortcuts run "Start Focus Timer"`,
				},
				Platforms: mac,
				Run:       runShortcutsRun,
			},
		)
	})
}

func runShortcutsList(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "folder")
	if err != nil {
		return err
	}
	if err := set.Reject("folder", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`shortcuts list` takes no positional arguments")
	}
	cmd := []string{"list", "--show-identifiers"}
	if f := set.String("folder", ""); f != "" {
		cmd = append(cmd, "--folder-name", f)
	}
	out, err := sys.Output("shortcuts", cmd...)
	if err != nil {
		return err
	}
	list := parseShortcutsList(out)
	if set.Has("json") {
		if list == nil {
			list = []shortcutInfo{}
		}
		return writeJSON(c, list)
	}
	for _, s := range list {
		c.Println(s.Name)
	}
	return nil
}

func runShortcutsRun(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "input", "text")
	if err != nil {
		return err
	}
	if err := set.Reject("input", "text"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`shortcuts run` needs one shortcut name or id; quote names with spaces")
	}
	if set.Has("input") && set.Has("text") {
		return fmt.Errorf("give --input or --text, not both")
	}
	dir, err := os.MkdirTemp("", "aos-shortcut-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	input := ""
	if p := set.String("input", ""); p != "" {
		input = expandHome(p)
		if _, err := os.Stat(input); err != nil {
			return fmt.Errorf("no such input file: %s", input)
		}
	} else if set.Has("text") {
		input = filepath.Join(dir, "input.txt")
		if err := os.WriteFile(input, []byte(set.String("text", "")), 0o600); err != nil {
			return err
		}
	}
	output := filepath.Join(dir, "output")
	if _, err := sys.Output("shortcuts", shortcutsRunArgs(set.Rest[0], input, output)...); err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("shortcut %q failed: %v", set.Rest[0], err)}
	}
	if data, err := os.ReadFile(output); err == nil && len(data) > 0 {
		c.Printf("%s", data)
		if data[len(data)-1] != '\n' {
			c.Println()
		}
	}
	return nil
}

// shortcutsRunArgs builds `shortcuts run`, writing the result as text to
// output so it can be printed whatever the shortcut returns.
func shortcutsRunArgs(name, input, output string) []string {
	args := []string{"run", name}
	if input != "" {
		args = append(args, "--input-path", input)
	}
	return append(args, "--output-path", output, "--output-type", "public.plain-text")
}

var shortcutLine = regexp.MustCompile(`^(.*) \(([0-9A-Fa-f-]{36})\)$`)

// parseShortcutsList reads `shortcuts list --show-identifiers`: "Name (UUID)".
func parseShortcutsList(out string) []shortcutInfo {
	var list []shortcutInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if m := shortcutLine.FindStringSubmatch(line); m != nil {
			list = append(list, shortcutInfo{Name: m[1], ID: m[2]})
			continue
		}
		list = append(list, shortcutInfo{Name: line})
	}
	return list
}
