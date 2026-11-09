package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// `watch window --app=<name> --match=<text>` (or --gone) is the text-condition
// form of watch window: it polls the window's text through the same reader
// `window text` uses and exits 0 when the condition holds. What to do next
// (notify, reply, run something) is the caller's job: `&& <agent acts>`.

// Each tick is a full accessibility walk of a window, hence the floor.
const (
	defaultTextWatchInterval = 2 * time.Second
	minTextWatchInterval     = time.Second
)

// textCond is the condition a text watch waits for.
type textCond struct {
	match string // met when this text is in the window
	gone  string // met when this text is not in the window
	exact bool   // a whole line equals it, instead of containing it
	fresh bool   // match only counts text that was not there at the first read
}

// parseTextCond validates --match / --gone: exactly one of them.
func parseTextCond(match, gone string, exact, fresh bool) (textCond, error) {
	switch {
	case match != "" && gone != "":
		return textCond{}, fmt.Errorf("use --match or --gone, not both")
	case match == "" && gone == "":
		return textCond{}, fmt.Errorf("give --match=<text> (wait for it to appear) or --gone=<text> (wait for it to disappear)")
	}
	if fresh && gone != "" {
		return textCond{}, fmt.Errorf("--new goes with --match")
	}
	return textCond{match: match, gone: gone, exact: exact, fresh: fresh}, nil
}

// textHas reports whether text holds want: a substring, ignoring case, or with
// exact a whole line.
func textHas(text, want string, exact bool) bool {
	if !exact {
		return textMatches(text, want, false)
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), want) {
			return true
		}
	}
	return false
}

// textState follows one watch across reads.
type textState struct {
	cond  textCond
	armed bool // --new: the text has been seen absent, so a hit now is new
}

// observe takes one read and reports whether the condition now holds. open is
// whether the window exists: a closed window has no text, so --gone is met by
// it and --match keeps waiting for it to come back.
func (s *textState) observe(text string, open bool) bool {
	if s.cond.gone != "" {
		return !open || !textHas(text, s.cond.gone, s.cond.exact)
	}
	has := open && textHas(text, s.cond.match, s.cond.exact)
	if s.cond.fresh && !s.armed {
		if !has {
			s.armed = true
		}
		return false
	}
	return has
}

// textWatchEvent is the JSON line printed when the condition is met.
type textWatchEvent struct {
	Event    string `json:"event"`
	At       string `json:"at"`
	Seq      int    `json:"seq"`
	App      string `json:"app,omitempty"`
	Title    string `json:"title,omitempty"`
	WindowID string `json:"window_id,omitempty"`
	Match    string `json:"match,omitempty"`
	Gone     string `json:"gone,omitempty"`
	Line     string `json:"line,omitempty"` // the line of text that matched
}

// textWindow is a seam for tests: read the text of the first window match
// selects. ok is false when there is no such window.
var textWindow = func(match engine.Match, source engine.FindSource) (engine.WindowText, bool, error) {
	w, ok := findWindow(match)
	if !ok {
		return engine.WindowText{}, false, nil
	}
	// Same preparation as `window text`: a Chromium window shows nothing to
	// accessibility until asked, and the ask is undone afterwards.
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
		Source: source, NoRaise: true, Timeout: 5 * time.Second,
	})
	return t, true, err
}

// runWatchWindowText is runWatchWindow's text mode; set holds the parsed flags.
func runWatchWindowText(c *cli.Ctx, set *argSet) error {
	if err := set.Reject("interval", "max", "app", "title", "match", "gone", "exact", "new", "timeout", "source", "json"); err != nil {
		return err
	}
	if set.Has("max") {
		return fmt.Errorf("--max is for focus changes; the text watch ends when its condition is met")
	}
	cond, err := parseTextCond(set.String("match", ""), set.String("gone", ""), set.Has("exact"), set.Has("new"))
	if err != nil {
		return err
	}
	if set.String("app", "")+set.String("title", "") == "" {
		return fmt.Errorf("name the window to read: --app=<name> or --title=<substring>")
	}
	match := engine.Match{App: set.String("app", ""), Title: set.String("title", "")}
	interval, err := parseInterval(set.String("interval", ""), defaultTextWatchInterval)
	if err != nil {
		return err
	}
	if interval < minTextWatchInterval {
		return fmt.Errorf("--interval for a text watch must be at least %s", minTextWatchInterval)
	}
	var timeout time.Duration
	if v := set.String("timeout", ""); v != "" {
		if timeout, err = parseInterval(v, 0); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	source := engine.FindSource(set.String("source", ""))
	// A bare app name may be an alias (Teams, Chrome); resolve it once, but
	// tolerate the app not running yet, since --match may be waiting for it.
	if resolved, err := resolveMatch(match); err == nil {
		match = resolved
	}
	read := func() (engine.WindowText, bool, error) { return textWindow(match, source) }
	return textWatchLoop(c, cond, interval, timeout, set.Has("json"), describeMatch(match), read)
}

func textWatchLoop(c *cli.Ctx, cond textCond, interval, timeout time.Duration, asJSON bool, target string,
	read func() (engine.WindowText, bool, error)) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	var deadline <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	state := &textState{cond: cond}
	for {
		t, open, err := read()
		if err != nil {
			c.Warnf("aos: %v\n", err)
		} else if state.observe(t.Text, open) {
			return printTextMet(c, cond, t, asJSON)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			what := fmt.Sprintf("%q did not appear in %s", cond.match, target)
			if cond.gone != "" {
				what = fmt.Sprintf("%q did not disappear from %s", cond.gone, target)
			}
			return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%s within %s", what, timeout)}
		case <-ticker.C:
		}
	}
}

func printTextMet(c *cli.Ctx, cond textCond, t engine.WindowText, asJSON bool) error {
	ev := textWatchEvent{
		Event: "window-text", At: time.Now().Format(time.RFC3339), Seq: 1,
		App: t.Window.App, Title: t.Window.Title, WindowID: t.Window.ID,
		Match: cond.match, Gone: cond.gone,
	}
	if cond.match != "" {
		for _, line := range strings.Split(t.Text, "\n") {
			if textHas(line, cond.match, cond.exact) {
				ev.Line = strings.TrimSpace(line)
				break
			}
		}
	}
	if asJSON {
		line, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		c.Printf("%s\n", line)
		return nil
	}
	if cond.gone != "" {
		c.Printf("%q is gone from %s\n", cond.gone, ev.App)
		return nil
	}
	c.Printf("%q appeared in %s: %s\n", cond.match, ev.App, ev.Line)
	return nil
}
