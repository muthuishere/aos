package groups

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/windowctl"
)

// exitNotFound is what `find` (and `draw --find`) exit with when the window or
// the text is not there. It matches windowctl's own CLI, so an agent reading
// exit codes gets the same answer from either tool, and it stays distinct from
// 1 (ran and failed) and 2 (refused before doing anything).
const exitNotFound = 3

func init() {
	register(func(r *cli.Registry) {
		r.Describe("find", "Find a button, menu item, link or text inside a window")
		r.Add(&cli.Command{
			Group:   "find",
			Summary: "Find text inside a window and print its global bounds and center",
			Args:    "(<app> | --app=<name> | --title=<substring>) --text=<text> [--source=auto|accessibility|ocr] [--role=<role>] [--exact] [--all] [--no-raise] [--json]",
			Examples: []string{
				`aos find --app=Calculator --text=7`,
				`aos find Safari --text="Reload this page" --role=button --json`,
				`aos find --title=Settings --text=Wi-Fi --source=ocr --all`,
				"# exit 3 when the window or the text is not found",
			},
			Run: runFind,
		})
	})
}

// findOptionsFrom turns `find` flags into windowctl's options. Kept apart from
// the runner so flag handling is testable without a screen.
func findOptionsFrom(args []string) (windowctl.FindOptions, *argSet, error) {
	set, err := parseArgs(args, "app", "title", "text", "source", "role")
	if err != nil {
		return windowctl.FindOptions{}, nil, err
	}
	if err := set.Reject("app", "title", "text", "source", "role", "exact", "all", "no-raise", "json"); err != nil {
		return windowctl.FindOptions{}, nil, err
	}
	match, err := matchFrom(set)
	if err != nil {
		return windowctl.FindOptions{}, nil, err
	}
	text := set.String("text", "")
	if strings.TrimSpace(text) == "" {
		return windowctl.FindOptions{}, nil, fmt.Errorf("--text is required: the text to find")
	}
	return windowctl.FindOptions{
		Match:   match,
		Text:    text,
		Source:  windowctl.FindSource(set.String("source", "")),
		Role:    set.String("role", ""),
		Exact:   set.Has("exact"),
		NoRaise: set.Has("no-raise"),
	}, set, nil
}

// notFoundExit maps windowctl's ErrNoMatch onto exit 3 with its message (which
// lists the closest labels it did see); anything else passes through as exit 1.
func notFoundExit(err error) error {
	if errors.Is(err, windowctl.ErrNoMatch) {
		return &cli.ExitError{Code: exitNotFound, Message: err.Error()}
	}
	return err
}

func runFind(c *cli.Ctx, args []string) error {
	opts, set, err := findOptionsFrom(args)
	if err != nil {
		return err
	}
	if resolved, err := resolveMatch(opts.Match); err == nil {
		opts.Match = resolved
	}
	hits, err := windowctl.FindText(opts)
	if err != nil {
		return notFoundExit(err)
	}
	if len(hits) > 0 {
		r := hits[0].Bounds
		noteVideo(c, "action", "find", &r, hits[0].Label)
	}
	if !set.Has("all") && len(hits) > 1 {
		hits = hits[:1]
	}
	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		if set.Has("all") {
			return enc.Encode(hits)
		}
		return enc.Encode(hits[0])
	}
	for _, h := range hits {
		c.Printf("%d %d  %dx%d%s%s  %-13s %-9s %q\n", h.Center.X, h.Center.Y,
			h.Bounds.W, h.Bounds.H, signed(h.Bounds.X), signed(h.Bounds.Y), h.Source, orDash(h.Role), h.Label)
	}
	return nil
}
