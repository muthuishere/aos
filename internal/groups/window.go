package groups

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("window", "Desktop windows: list, focus, move, resize, arrange")
		r.Add(
			&cli.Command{
				Group: "window", Name: "list",
				Summary: "List open windows",
				Args:    "[--app=<name>] [--title=<substring>] [--json]",
				Examples: []string{
					"aos window list",
					"aos window list --app=Chrome",
				},
				Run: runWindowList,
			},
			&cli.Command{
				Group: "window", Name: "focus",
				Summary:  "Bring a window to the front",
				Args:     "<app> | --app=<name> | --title=<substring>",
				Examples: []string{"aos window focus Chrome"},
				Run:      runWindowFocus,
			},
			&cli.Command{
				Group: "window", Name: "move",
				Summary: "Move a window to a zone, a split, or absolute bounds",
				Args:    "<app> (--zone=<1A|2:1> | --at=<x,y,w,h>) [--monitor=<n>]",
				Examples: []string{
					"aos window move Chrome --zone=1B",
					"aos window move Chrome --monitor=2 --zone=1:3",
					"aos window move Slack --at=0,25,1920,1055",
				},
				Run: runWindowMove,
			},
			&cli.Command{
				Group: "window", Name: "resize",
				Summary:  "Resize a window in place",
				Args:     "<app> --w=<width> --h=<height>",
				Examples: []string{"aos window resize Chrome --w=900 --h=700"},
				Run:      runWindowResize,
			},
			&cli.Command{
				Group: "window", Name: "wait",
				Summary:  "Wait for a window to appear, then print it",
				Args:     "<app> [--timeout=<ms>]",
				Examples: []string{"aos window wait Chrome --timeout=5000"},
				Run:      runWindowWait,
			},
			&cli.Command{
				Group: "window", Name: "zones",
				Summary: "Show what each zone means on a real monitor",
				Args:    "[--monitor=<n>] [--show]",
				Examples: []string{
					"aos window zones",
					"aos window zones --monitor=2",
					"aos window zones --show --app=Chrome",
				},
				Run: runWindowZones,
			},
			&cli.Command{
				Group: "window", Name: "arrange",
				Summary: "Apply a saved layout of many windows in one pass",
				Args:    "<layout.json | ->",
				Examples: []string{
					"aos window arrange ~/.config/aos/layouts/work.json",
					`echo '[{"app":"Google Chrome","zone":"1A"},{"app":"Slack","zone":"1B"}]' | aos window arrange -`,
				},
				Run: runWindowArrange,
			},
			&cli.Command{
				Group: "window", Name: "save",
				Summary: "Save the current layout to a file `window arrange` can apply",
				Args:    "<layout.json> [--app=<name>] [--monitor=<n>]",
				Examples: []string{
					"aos window save ~/.config/aos/layouts/work.json",
					"aos window save ~/work.json --app=Chrome",
					"aos window save ~/second-screen.json --monitor=2",
				},
				NeedsDisplay: true,
				Run:          runWindowSave,
			},
		)
	})
}

// matchFrom reads the --app / --title pair every window command accepts. A bare
// positional is treated as an application name, which is the common intent.
func matchFrom(set *argSet) (engine.Match, error) {
	match := engine.Match{
		App:   set.String("app", ""),
		Title: set.String("title", ""),
	}
	if match.App == "" && match.Title == "" && len(set.Rest) > 0 {
		match.App = strings.Join(set.Rest, " ")
	}
	if match.App == "" && match.Title == "" {
		return match, fmt.Errorf("name a window: a bare app name, --app=<name>, or --title=<substring>")
	}
	return match, nil
}

func runWindowList(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title", "json"); err != nil {
		return err
	}

	filter := engine.Filter{
		App:   set.String("app", ""),
		Title: set.String("title", ""),
	}
	// A bare positional means the same thing here as it does everywhere else.
	// It used to be dropped on the floor, so `window list Chrome` quietly
	// listed every window on the machine.
	if filter.App == "" && filter.Title == "" && len(set.Rest) > 0 {
		filter.App = strings.Join(set.Rest, " ")
	}
	if filter.App != "" {
		if app, ok := resolveAppName(filter.App); ok {
			filter.App = app
		}
	}

	windows, err := engine.ListWindows(filter)
	if err != nil {
		return err
	}

	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(windows)
	}
	if len(windows) == 0 {
		c.Println("no windows matched")
		return &cli.ExitError{Code: 1}
	}

	width := 0
	for _, w := range windows {
		if len(w.App) > width {
			width = len(w.App)
		}
	}
	for _, w := range windows {
		focus := " "
		if w.Focused {
			focus = "*"
		}
		c.Printf("%s %-*s  mon %d  %dx%d%s%s  %s\n",
			focus, width, w.App, w.Monitor,
			w.Bounds.W, w.Bounds.H, signed(w.Bounds.X), signed(w.Bounds.Y),
			w.Title)
	}
	return nil
}

// findWindow returns the first window a match selects.
func findWindow(match engine.Match) (engine.Window, bool) {
	windows, err := engine.ListWindows(engine.Filter(match))
	if err != nil || len(windows) == 0 {
		return engine.Window{}, false
	}
	return windows[0], true
}

// resolveMatch turns a user's app-or-title guess into one the window backend
// can act on.
//
// Not every platform reports an application name: on X11 the window list comes
// from `wmctrl -lpG`, which has no WM_CLASS column, so the App field carries the
// client's hostname instead. A bare `aos window focus xterm` therefore
// has to fall back to matching the title, or it would work on macOS and never
// on Linux.
func resolveMatch(match engine.Match) (engine.Match, error) {
	if _, ok := findWindow(match); ok {
		return match, nil
	}
	if match.App != "" && match.Title == "" {
		if app, ok := resolveAppName(match.App); ok {
			return engine.Match{App: app}, nil
		}
		byTitle := engine.Match{Title: match.App}
		if _, ok := findWindow(byTitle); ok {
			return byTitle, nil
		}
	}
	return match, fmt.Errorf("no window matched %s", describeMatch(match))
}

// resolveAppName turns what a person or an agent actually types into the
// application name this platform reports.
//
// The backend matches App exactly and case-sensitively, which makes the most
// natural thing to write the one thing that cannot work: on macOS the browser
// is "Google Chrome", so `--app=Chrome` matched nothing at all — while
// `--title=` had always been a substring. Every example in the docs and in the
// bundled agent skill is written the short way, because that is how people say
// it, so the front door resolves the short way to the real name instead.
//
// An exact hit wins over a substring one, so a machine running both "Code" and
// "Code - Insiders" still resolves `--app=Code` to the one that was named.
func resolveAppName(query string) (string, bool) {
	if strings.TrimSpace(query) == "" {
		return "", false
	}
	windows, err := engine.ListWindows(engine.Filter{})
	if err != nil {
		return "", false
	}
	names := make([]string, 0, len(windows))
	for _, w := range windows {
		names = append(names, w.App)
	}
	return pickAppName(names, query)
}

// pickAppName is the resolution itself, split out from the window backend so
// it can be tested without a display.
func pickAppName(names []string, query string) (string, bool) {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return "", false
	}
	for _, name := range names {
		if strings.EqualFold(name, query) {
			return name, true
		}
	}
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), q) {
			return name, true
		}
	}
	return "", false
}

func describeMatch(match engine.Match) string {
	var parts []string
	if match.App != "" {
		parts = append(parts, "app "+match.App)
	}
	if match.Title != "" {
		parts = append(parts, "title "+match.Title)
	}
	return strings.Join(parts, " and ")
}

func runWindowFocus(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title"); err != nil {
		return err
	}
	match, err := matchFrom(set)
	if err != nil {
		return err
	}
	resolved, err := resolveMatch(match)
	if err != nil {
		return err
	}
	return engine.Focus(resolved)
}

func runWindowMove(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title", "zone", "at", "monitor")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title", "zone", "at", "monitor"); err != nil {
		return err
	}
	match, err := matchFrom(set)
	if err != nil {
		return err
	}
	match, err = resolveMatch(match)
	if err != nil {
		return err
	}
	monitor, err := set.IntPtr("monitor")
	if err != nil {
		return err
	}

	zone, at := set.String("zone", ""), set.String("at", "")
	switch {
	case zone != "" && at != "":
		return fmt.Errorf("give either --zone or --at, not both")
	case zone != "":
		return engine.MoveZone(match, monitor, zone)
	case at != "":
		bounds, err := parseRect(at)
		if err != nil {
			return err
		}
		return engine.MoveCoords(match, monitor, bounds)
	case monitor != nil:
		// Monitor alone means "send it there and fill the screen", spelled 1:1
		// because there is no "full" zone. Filling the screen always trips the
		// clamp check — every OS reserves a menu bar or taskbar — and the caller
		// asked for a monitor, not for exact bounds, so that is not a failure.
		if err := engine.MoveZone(match, monitor, "1:1"); err != nil &&
			!strings.Contains(err.Error(), "OS clamped") {
			return err
		}
		return nil
	}
	return fmt.Errorf("say where: --zone=<1A|2:1>, --at=<x,y,w,h>, or --monitor=<n>")
}

// parseRect reads the `x,y,w,h` form used by --at and --region.
func parseRect(value string) (engine.Rect, error) {
	parts := strings.Split(value, ",")
	if len(parts) != 4 {
		return engine.Rect{}, fmt.Errorf("expected x,y,w,h — got %q", value)
	}
	numbers := make([]int, 4)
	for i, part := range parts {
		n, err := parseInt(strings.TrimSpace(part))
		if err != nil {
			return engine.Rect{}, fmt.Errorf("bad number %q in %q", part, value)
		}
		numbers[i] = n
	}
	return engine.Rect{X: numbers[0], Y: numbers[1], W: numbers[2], H: numbers[3]}, nil
}

func runWindowResize(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title", "w", "h")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title", "w", "h"); err != nil {
		return err
	}
	match, err := matchFrom(set)
	if err != nil {
		return err
	}
	width, err := set.Int("w", 0)
	if err != nil {
		return err
	}
	height, err := set.Int("h", 0)
	if err != nil {
		return err
	}
	if width <= 0 || height <= 0 {
		return fmt.Errorf("--w and --h must both be positive")
	}
	resolved, err := resolveMatch(match)
	if err != nil {
		return err
	}
	return engine.Resize(resolved, width, height)
}

func runWindowWait(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title", "timeout"); err != nil {
		return err
	}
	match, err := matchFrom(set)
	if err != nil {
		return err
	}
	timeout, err := set.Int("timeout", 10000)
	if err != nil {
		return err
	}
	window, err := waitForMatch(match, timeout)
	if err != nil {
		return err
	}
	c.Println(describeWindow(window))
	return nil
}

// waitForMatch polls until a window matches, trying the title fallback on each
// pass so a window that only ever appears under its title is still caught.
func waitForMatch(match engine.Match, timeoutMS int) (engine.Window, error) {
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		if resolved, err := resolveMatch(match); err == nil {
			if window, ok := findWindow(resolved); ok {
				return window, nil
			}
		}
		if time.Now().After(deadline) {
			return engine.Window{}, fmt.Errorf("no window matched %s within %dms",
				describeMatch(match), timeoutMS)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func describeWindow(w engine.Window) string {
	return fmt.Sprintf("%s  mon %d  %dx%d%s%s  %s",
		w.App, w.Monitor, w.Bounds.W, w.Bounds.H, signed(w.Bounds.X), signed(w.Bounds.Y), w.Title)
}

func runWindowArrange(c *cli.Ctx, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("`window arrange` takes one layout file (or - for stdin)")
	}
	var data []byte
	var err error
	if args[0] == "-" {
		data, err = io.ReadAll(c.Stdin)
	} else {
		data, err = os.ReadFile(expandHome(args[0]))
	}
	if err != nil {
		return err
	}
	entries, err := parseLayout(data)
	if err != nil {
		return err
	}

	// Batch never aborts on a failing entry, so report per-entry and fail at
	// the end only if something actually went wrong.
	failed := 0
	for _, result := range engine.Batch(entries) {
		name := result.Entry.App
		if name == "" {
			name = result.Entry.Title
		}
		if result.Err != nil {
			failed++
			c.Printf("fail  %s: %v\n", name, result.Err)
			continue
		}
		c.Printf("ok    %s\n", name)
	}
	if failed > 0 {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%d of %d entries failed", failed, len(entries))}
	}
	return nil
}

// parseLayout reads a layout file into the entries `window arrange` applies.
// `window save` writes through layoutFrom, so this is the other half of the
// round trip and both sides stay honest about the shape.
func parseLayout(data []byte) ([]engine.BatchEntry, error) {
	var entries []engine.BatchEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	return entries, nil
}

// layoutFrom turns a live window list into layout entries. It is the whole of
// `window save` that does not touch a display, so it is the part worth testing.
//
// Bounds are written absolute and the monitor is deliberately left out: a
// BatchEntry carrying a monitor has its X/Y read as monitor-relative, so
// stamping the monitor next to global coordinates would offset every window by
// that monitor's origin on the way back.
//
// The title is only written when the app name alone would be ambiguous. Titles
// drift — a browser retitles itself with every tab — so pinning one costs a
// match later, and it is only worth paying when two saved windows share an app.
func layoutFrom(windows []engine.Window) []engine.BatchEntry {
	perApp := map[string]int{}
	for _, w := range windows {
		perApp[w.App]++
	}

	entries := make([]engine.BatchEntry, 0, len(windows))
	for _, w := range windows {
		// Arrange rejects a zero-sized entry, so a window that reports no
		// size is dropped here rather than saved as a guaranteed failure.
		if w.Bounds.W <= 0 || w.Bounds.H <= 0 {
			continue
		}
		entry := engine.BatchEntry{
			App: w.App,
			X:   intPtr(w.Bounds.X),
			Y:   intPtr(w.Bounds.Y),
			W:   intPtr(w.Bounds.W),
			H:   intPtr(w.Bounds.H),
		}
		if w.App == "" || perApp[w.App] > 1 {
			entry.Title = w.Title
		}
		if entry.App == "" && entry.Title == "" {
			continue
		}
		entries = append(entries, entry)
	}
	return entries
}

func intPtr(n int) *int { return &n }

// runWindowSave captures what is on screen right now as a layout file, which
// is the missing half of `window arrange`: until this existed the only way to
// get a layout was to hand-write the JSON.
func runWindowSave(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "monitor")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "monitor"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`window save` takes one layout file")
	}
	monitor, err := set.IntPtr("monitor")
	if err != nil {
		return err
	}

	filter := engine.Filter{App: set.String("app", "")}
	if filter.App != "" {
		if app, ok := resolveAppName(filter.App); ok {
			filter.App = app
		}
	}
	windows, err := engine.ListWindows(filter)
	if err != nil {
		return err
	}
	if monitor != nil {
		kept := make([]engine.Window, 0, len(windows))
		for _, w := range windows {
			if w.Monitor == *monitor {
				kept = append(kept, w)
			}
		}
		windows = kept
	}

	entries := layoutFrom(windows)
	if len(entries) == 0 {
		c.Println("no windows matched — nothing saved")
		return &cli.ExitError{Code: 1}
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	path := expandHome(set.Rest[0])
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return err
	}
	c.Printf("saved %d window(s) to %s\n", len(entries), path)
	return nil
}

// namedZones are the halves and quarters, plus the whole screen as 1:1.
var namedZones = []string{"1:1", "1A", "1B", "2A", "2B", "2C", "2D"}

// exampleSplits stand in for the unbounded N:M form: N parts, the M-th of them.
var exampleSplits = []string{"1:2", "2:2", "1:3", "2:3", "3:3"}

// runWindowZones prints each zone's real rectangle on a real monitor, because
// "1A" means nothing until you see that it is the left half of *this* screen.
// With --show it walks a window through them, which is the only way to actually
// feel the difference.
func runWindowZones(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "monitor", "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("monitor", "app", "title", "show"); err != nil {
		return err
	}
	monitorID, err := set.IntPtr("monitor")
	if err != nil {
		return err
	}

	monitors, err := engine.ListMonitors()
	if err != nil {
		return err
	}
	if len(monitors) == 0 {
		return fmt.Errorf("no monitors detected")
	}
	target := monitors[0]
	for _, m := range monitors {
		if (monitorID != nil && m.ID == *monitorID) || (monitorID == nil && m.Focused) {
			target = m
		}
	}

	c.Printf("monitor %d  %dx%d%s%s\n\n", target.ID,
		target.Width, target.Height, signed(target.X), signed(target.Y))

	for _, name := range append(append([]string{}, namedZones...), exampleSplits...) {
		zone, err := engine.ParseZone(name)
		if err != nil {
			continue
		}
		r := zone.Rect(target)
		c.Printf("  %-6s %dx%d%s%s\n", name, r.W, r.H, signed(r.X), signed(r.Y))
	}
	c.Println()
	c.Println("  M:N   the M-th of N: 1:3 is the left third, 2:3 the middle, 3:3 the right")
	c.Println("        the other order works too, so 3:1 and 1:3 are the same zone")

	if !set.Has("show") {
		c.Println()
		c.Println("Add --show --app=<name> to walk a window through them.")
		return nil
	}

	match, err := matchFrom(set)
	if err != nil {
		return err
	}
	resolved, err := resolveMatch(match)
	if err != nil {
		return err
	}
	for _, name := range namedZones {
		if err := engine.MoveZone(resolved, &target.ID, name); err != nil {
			c.Warnf("%s: %v\n", name, err)
			continue
		}
		c.Printf("  showing %s\n", name)
		time.Sleep(700 * time.Millisecond)
	}
	return nil
}
