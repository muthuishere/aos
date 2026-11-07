package groups

import (
	"fmt"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// menuItem is one leaf of an app's menu bar, in `menu list --json` shape.
type menuItem struct {
	Path    string `json:"path"` // "File > Export as PDF…"
	Enabled bool   `json:"enabled"`
}

// menuSep joins menu titles into a path.
const menuSep = " > "

func init() {
	register(func(r *cli.Registry) {
		r.Describe("menu", "Search and run an app's menu bar commands (macOS accessibility)")
		mac := []string{"darwin"}
		r.Add(
			&cli.Command{
				Group: "menu", Name: "list",
				Summary: "List every menu command of an app (frontmost by default) as a path",
				Args:    "[--app=<name>] [--filter=<substring>] [--json]",
				Examples: []string{
					"aos menu list --app=Preview",
					"aos menu list --filter=export",
					"aos menu list --app=Safari --json",
				},
				Platforms: mac,
				Run:       runMenuList,
			},
			&cli.Command{
				Group: "menu", Name: "run",
				Summary: "Run a menu command by path; exact path wins, else a unique substring",
				Args:    "<path> [--app=<name>]",
				Examples: []string{
					`aos menu run "File > Export as PDF…" --app=Preview`,
					`aos menu run "export as pdf" --app=Preview   # unique substring`,
				},
				Platforms: mac,
				Run:       runMenuRun,
			},
		)
	})
}

func runMenuList(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "filter")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "filter", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`menu list` takes no positional arguments; use --filter")
	}
	items, err := listMenu(set.String("app", ""))
	if err != nil {
		return err
	}
	if f := strings.ToLower(set.String("filter", "")); f != "" {
		var kept []menuItem
		for _, it := range items {
			if strings.Contains(strings.ToLower(it.Path), f) {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	if set.Has("json") {
		if items == nil {
			items = []menuItem{}
		}
		return writeJSON(c, items)
	}
	for _, it := range items {
		if it.Enabled {
			c.Println(it.Path)
		} else {
			c.Println(it.Path + "  (disabled)")
		}
	}
	return nil
}

func runMenuRun(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app")
	if err != nil {
		return err
	}
	if err := set.Reject("app"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`menu run` needs one menu path; quote it, e.g. \"File > Save\"")
	}
	app := set.String("app", "")
	items, err := listMenu(app)
	if err != nil {
		return err
	}
	item, err := matchMenu(items, set.Rest[0])
	if err != nil {
		return err
	}
	if !item.Enabled {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%q is disabled right now", item.Path)}
	}
	if err := pressMenu(app, item.Path); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	c.Printf("ran %s\n", item.Path)
	return nil
}

// normalizeMenuPath lowercases and canonicalises separators and ellipses so
// "file>export as pdf..." and "File > Export as PDF…" compare equal.
func normalizeMenuPath(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "…", "..."))
	parts := strings.Split(s, ">")
	for i, p := range parts {
		parts[i] = strings.Join(strings.Fields(p), " ")
	}
	return strings.Join(parts, menuSep)
}

// matchMenu resolves a query to one item: an exact (normalised) path wins;
// otherwise a substring of the full path must match exactly one item.
// Running the wrong command is not undoable, so ambiguity is refused.
func matchMenu(items []menuItem, query string) (menuItem, error) {
	q := normalizeMenuPath(query)
	if strings.TrimSpace(q) == "" {
		return menuItem{}, fmt.Errorf("empty menu path")
	}
	var hits []menuItem
	for _, it := range items {
		n := normalizeMenuPath(it.Path)
		if n == q {
			return it, nil
		}
		if strings.Contains(n, q) {
			hits = append(hits, it)
		}
	}
	switch len(hits) {
	case 0:
		return menuItem{}, &cli.ExitError{Code: 1, Message: fmt.Sprintf("no menu command matches %q; see `aos menu list`", query)}
	case 1:
		return hits[0], nil
	}
	var lines []string
	for i, h := range hits {
		if i == 10 {
			lines = append(lines, fmt.Sprintf("  ... and %d more", len(hits)-10))
			break
		}
		lines = append(lines, "  "+h.Path)
	}
	return menuItem{}, &cli.ExitError{Code: 1, Message: fmt.Sprintf("%q matches %d menu commands; give the full path:\n%s", query, len(hits), strings.Join(lines, "\n"))}
}

// parseMenuDump reads the backend's "enabled<TAB>title<TAB>title..." lines.
func parseMenuDump(out string) []menuItem {
	var items []menuItem
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		items = append(items, menuItem{Enabled: f[0] == "1", Path: strings.Join(f[1:], menuSep)})
	}
	return items
}
