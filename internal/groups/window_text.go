package groups

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "window", Name: "text",
			Summary: "Print all the text of a window, in reading order (accessibility, OCR fallback)",
			Args:    "(<app> | --app=<name> | --title=<substring> | --id=<window id> | --focused) [--source=auto|accessibility|ocr] [--json] [--plain] [--max-chars=<n>] [--timeout=<ms>] [--raise]",
			Examples: []string{
				"aos window text TextEdit",
				"aos window text --focused --max-chars=4000",
				"aos window text --app=Chrome --json",
				"aos window text --id=12345 --source=ocr --raise   # OCR a covered window: brings it to the front, then hands focus back",
				"# auto reads the accessibility tree (AX / UI Automation / AT-SPI) and falls back to OCR when it has almost no text",
				"# exit 3 when no window matches",
			},
			Run: runWindowText,
		})
	})
}

// windowTextFlags: the value flags `window text` takes.
var windowTextFlags = []string{"app", "title", "id", "source", "max-chars", "timeout"}

func runWindowText(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, windowTextFlags...)
	if err != nil {
		return err
	}
	if err := set.Reject(append(windowTextFlags, "focused", "json", "plain", "no-raise", "raise", "force")...); err != nil {
		return err
	}
	maxChars, err := set.Int("max-chars", 0)
	if err != nil {
		return err
	}
	timeout, err := set.Int("timeout", 5000)
	if err != nil {
		return err
	}
	if set.Has("raise") {
		if err := politeCheck(c, set, "window text --raise"); err != nil {
			return err
		}
	}
	w, err := windowForText(set)
	if err != nil {
		return err
	}
	// A Chromium window shows nothing below its toolbar to accessibility
	// until asked; prepare it (and undo that afterwards) like tab read does.
	// When the page still is not there, auto mode reads it from the pixels.
	source := engine.FindSource(set.String("source", ""))
	if windowKind(w.App) == "browser" && source != engine.FindOCR {
		restore, perr := engine.WebPrepare(w)
		if restore != nil {
			defer restore()
		}
		if perr != nil && (source == "" || source == engine.FindAuto) {
			source = engine.FindOCR
		}
	}
	t, err := engine.ReadWindowText(w, engine.WindowTextOptions{
		Source:   source,
		NoRaise:  !set.Has("raise"),
		MaxChars: maxChars,
		Timeout:  time.Duration(timeout) * time.Millisecond,
	})
	if err != nil {
		return err
	}
	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(t)
	}
	if t.Text != "" {
		c.Println(t.Text)
	}
	return nil
}

// windowForText picks the one window `window text` reads: by ID, the
// focused one, or the usual app/title match. Not found is exit 3, like find.
func windowForText(set *argSet) (engine.Window, error) {
	id, focused := set.String("id", ""), set.Has("focused")
	if id != "" || focused {
		ws, err := engine.ListWindows(engine.Filter{})
		if err != nil {
			return engine.Window{}, err
		}
		for _, w := range ws {
			if (id != "" && w.ID == id) || (id == "" && w.Focused) {
				return w, nil
			}
		}
		what := "no window has id " + id
		if id == "" {
			what = "no window is focused"
		}
		return engine.Window{}, &cli.ExitError{Code: exitNotFound, Message: what}
	}
	match, err := matchFrom(set)
	if err != nil {
		return engine.Window{}, err
	}
	resolved, err := resolveMatch(match)
	if err != nil {
		return engine.Window{}, &cli.ExitError{Code: exitNotFound, Message: err.Error()}
	}
	w, ok := findWindow(resolved)
	if !ok {
		return engine.Window{}, &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("no window matched %s", describeMatch(match))}
	}
	return w, nil
}
