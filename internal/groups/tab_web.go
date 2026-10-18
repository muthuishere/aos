package groups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// Reading and driving the page inside a browser tab through accessibility:
// `tab read`, `tab type`, `tab click` and `watch tab`. The tab is selected in
// its window without raising the window, and nothing here takes focus: a
// click is an accessibility press, typing is key events posted to the
// browser's process.

func init() {
	register(func(r *cli.Registry) {
		platforms := []string{"darwin", "windows"}
		r.Add(
			&cli.Command{
				Group: "tab", Name: "read",
				Summary: "Read a browser tab's page as markdown, with refs for its buttons, links and fields",
				Args:    "--id=<tab id> [--md | --json]",
				Examples: []string{
					"aos tab read --id=chrome:1658108662:1658109078",
					"aos tab read --id=safari:22718:2 --json",
					"# [e7] Message (textarea, empty)   [e8] Send (button)",
				},
				Platforms: platforms,
				Run:       runTabRead,
			},
			&cli.Command{
				Group: "tab", Name: "type",
				Summary: "Type into a field of a browser tab's page without taking focus",
				Args:    "--id=<tab id> (--element=<ref> | --into=<label>) --text=<text> [--enter] [--exact] [--nth=<n>]",
				Examples: []string{
					`aos tab type --id=chrome:1658108662:1658109078 --element=e7 --text="hello" --enter`,
					`aos tab type --id=chrome:1658108662:1658109078 --into=Message --text="hello"`,
					"# exit 3 when no field matches or the page changed, 4 when several fields match",
				},
				Platforms: platforms,
				Run:       runTabType,
			},
			&cli.Command{
				Group: "tab", Name: "click",
				Summary: "Press a button or link in a browser tab's page, without moving the mouse",
				Args:    "--id=<tab id> (--element=<ref> | --find=<label>) [--role=button|link|checkbox|radio|...] [--exact] [--nth=<n>]",
				Examples: []string{
					"aos tab click --id=chrome:1658108662:1658109078 --element=e8",
					"aos tab click --id=chrome:1658108662:1658109078 --find=Send --role=button",
					"# exit 3 when nothing matches or the page changed, 4 when several elements match",
				},
				Platforms: platforms,
				Run:       runTabClick,
			},
			&cli.Command{
				Group: "watch", Name: "tab",
				Summary:  "Print a JSON line with the new text whenever a browser tab's page changes",
				Blocking: true,
				Args:     "--id=<tab id> [--match=<text>] [--exact] [--interval=<500ms>] [--timeout=<duration>] [--max=<n>]",
				Examples: []string{
					"aos watch tab --id=chrome:1658108662:1658109078",
					`aos watch tab --id=chrome:1658108662:1658109078 --match="Build finished" --timeout=5m`,
					"# with --match: exit 0 when the text appears, 3 on --timeout",
				},
				Platforms: platforms,
				Run:       runWatchTab,
			},
		)
	})
}

// Bounds on one read: a long chat or feed is thousands of nodes, and an
// accessibility round trip each; past this the page is cut, not stalled on.
const (
	pageMaxNodes = 4000
	pageMaxDepth = 64
)

// Seams for tests.
var (
	webTree   = engine.WebTree
	webAct    = engine.WebAct
	pageTab   = resolvePageTab
	typeInto  = engine.PostTextTo
	pressInto = engine.PostKeyTo
)

// quietSelector is a provider that can select a tab in its window without
// raising the window.
type quietSelector interface {
	selectQuiet(ctx context.Context, t Tab) error
}

// resolvePageTab finds a browser tab by id, selects it in its window if it
// is not already the selected one, and returns its window.
func resolvePageTab(id string) (Tab, engine.Window, error) {
	if id == "" {
		return Tab{}, engine.Window{}, fmt.Errorf("--id is required: a tab id from `aos tab list --kind=browser`")
	}
	t, err := pickTab(tabFilter{ID: id}, true)
	if err != nil {
		return Tab{}, engine.Window{}, err
	}
	if t.Kind != "browser" {
		return Tab{}, engine.Window{}, fmt.Errorf("%s is a %s tab; page commands work on browser tabs", t.ID, t.Kind)
	}
	if !t.Active {
		p, err := providerFor(t)
		if err != nil {
			return Tab{}, engine.Window{}, err
		}
		q, ok := p.(quietSelector)
		if !ok {
			return Tab{}, engine.Window{}, fmt.Errorf("aos cannot select %s tabs without raising the window; focus it first (`aos tab focus --id=%s`)", t.App, t.ID)
		}
		ctx, cancel := tabCtx()
		err = q.selectQuiet(ctx, t)
		cancel()
		if err != nil {
			return Tab{}, engine.Window{}, err
		}
		time.Sleep(250 * time.Millisecond) // the window swaps its web area
	}
	ws, err := engine.ListWindows(engine.Filter{})
	if err != nil {
		return Tab{}, engine.Window{}, err
	}
	pid := 0
	for _, w := range ws {
		if t.Window != "" && w.ID == t.Window {
			return t, w, nil
		}
		if w.App == t.App {
			pid = w.PID
		}
	}
	// The on-screen list leaves out windows on another Space. Accessibility
	// still reaches them, by the app's process and the window's title.
	title := t.windowTitle
	if title == "" {
		title = t.Title
	}
	if pid == 0 || title == "" {
		return Tab{}, engine.Window{}, &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%s: its window is not on screen (list again, or bring it to this Space)", t.ID)}
	}
	return t, engine.Window{App: t.App, Title: title, PID: pid}, nil
}

type pageRead struct {
	Tab      string        `json:"tab"`
	Title    string        `json:"title"`
	URL      string        `json:"url,omitempty"`
	Markdown string        `json:"markdown"`
	Elements []pageElement `json:"elements"`
}

func runTabRead(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "md", "json"); err != nil {
		return err
	}
	if set.Has("md") && set.Has("json") {
		return fmt.Errorf("--md and --json are exclusive")
	}
	t, w, err := pageTab(set.String("id", ""))
	if err != nil {
		return err
	}
	nodes, err := webTree(&w, pageMaxNodes, pageMaxDepth)
	if err != nil {
		return err
	}
	md := strings.Join(renderPage(nodes, true, false), "\n")
	if set.Has("json") {
		elems := pageElements(nodes)
		if elems == nil {
			elems = []pageElement{}
		}
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(pageRead{Tab: t.ID, Title: t.Title, URL: t.URL, Markdown: md, Elements: elems})
	}
	if len(nodes) >= pageMaxNodes {
		c.Warnf("aos: the page has more than %d elements; the rest is cut\n", pageMaxNodes)
	}
	c.Println(md)
	return nil
}

// pickElement resolves --element or a label query to one element. A ref is
// checked against nothing here (the adapter re-verifies it on use); a label
// with several matches is refused with the candidates (exit 4) unless --nth
// picks one.
func pickElement(nodes []engine.WebNode, ref, query string, exact bool, nth int, keep func(pageElement) bool, what string) (pageElement, error) {
	elems := pageElements(nodes)
	if ref != "" {
		idx, err := parseElementRef(ref)
		if err != nil {
			return pageElement{}, err
		}
		for _, e := range elems {
			if e.index == idx {
				if keep != nil && !keep(e) {
					return pageElement{}, fmt.Errorf("%s is a %s, not a %s", ref, e.Role, what)
				}
				return e, nil
			}
		}
		return pageElement{}, &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%s is not on the page any more (the page changed); run `aos tab read` again", ref)}
	}
	hits := matchElements(elems, query, exact, keep)
	if len(hits) == 0 {
		msg := fmt.Sprintf("no %s matches %q", what, query)
		var near []string
		for _, e := range elems {
			if keep == nil || keep(e) {
				near = append(near, "  "+e.annotation(true))
			}
		}
		if len(near) > 12 {
			near = near[:12]
		}
		if len(near) > 0 {
			msg += "; on the page:\n" + strings.Join(near, "\n")
		}
		return pageElement{}, &cli.ExitError{Code: exitNotFound, Message: msg}
	}
	if nth > 0 {
		if nth > len(hits) {
			return pageElement{}, &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("--nth=%d but only %d %ss match %q", nth, len(hits), what, query)}
		}
		return hits[nth-1], nil
	}
	if len(hits) > 1 {
		msg := fmt.Sprintf("%d %ss match %q; name one with --element (or --exact / --nth):", len(hits), what, query)
		for _, e := range hits {
			msg += "\n  " + e.annotation(true)
		}
		return pageElement{}, &cli.ExitError{Code: exitAmbiguous, Message: msg}
	}
	return hits[0], nil
}

// actOn performs action on e, refusing (exit 3) when the element at its ref
// is not the one that was read.
func actOn(w engine.Window, e pageElement, action int) error {
	err := webAct(w, pageMaxNodes, pageMaxDepth, e.index, action, e.axRole, e.fp)
	if errors.Is(err, engine.ErrWebStale) {
		return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%s is no longer %q (%s): the page changed; run `aos tab read` again", e.Ref, e.Label, e.Role)}
	}
	return err
}

// targetFlags reads the "exactly one of --element / --<label flag>" pair.
func targetFlags(set *argSet, labelFlag string) (ref, query string, nth int, err error) {
	ref, query = set.String("element", ""), set.String(labelFlag, "")
	if (ref == "") == (query == "") {
		return "", "", 0, fmt.Errorf("give exactly one of --element (a ref from `aos tab read`) or --%s", labelFlag)
	}
	nth, err = set.Int("nth", 0)
	if err == nil && nth < 0 {
		err = fmt.Errorf("--nth must be 1 or more")
	}
	return ref, query, nth, err
}

func runTabType(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id", "element", "into", "text", "nth")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "element", "into", "text", "nth", "enter", "exact"); err != nil {
		return err
	}
	ref, query, nth, err := targetFlags(set, "into")
	if err != nil {
		return err
	}
	text := set.String("text", "")
	if text == "" && !set.Has("enter") {
		return fmt.Errorf("--text is required")
	}
	_, w, err := pageTab(set.String("id", ""))
	if err != nil {
		return err
	}
	nodes, err := webTree(&w, pageMaxNodes, pageMaxDepth)
	if err != nil {
		return err
	}
	e, err := pickElement(nodes, ref, query, set.Has("exact"), nth, func(e pageElement) bool { return pageEditable[e.Role] }, "field")
	if err != nil {
		return err
	}
	if err := actOn(w, e, engine.WebFocus); err != nil {
		return err
	}
	// Pages ignore values set through accessibility (React, Lexical keep
	// their own state), so the text goes in as key events posted to the
	// browser's process: the field has accessibility focus, the window stays
	// where it is, and the frontmost app never changes.
	if text != "" {
		if err := typeInto(w, text); err != nil {
			return err
		}
	}
	if set.Has("enter") {
		if err := pressInto(w, "enter"); err != nil {
			return err
		}
	}
	c.Printf("typed into %s  %s\n", e.Ref, e.annotation(false))
	return nil
}

func runTabClick(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id", "element", "find", "role", "nth")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "element", "find", "role", "nth", "exact"); err != nil {
		return err
	}
	ref, query, nth, err := targetFlags(set, "find")
	if err != nil {
		return err
	}
	role := strings.ToLower(set.String("role", ""))
	if role != "" && !pageActionable[role] {
		return fmt.Errorf("--role must be one of button, link, checkbox, radio, select, textbox, textarea, combobox; got %q", role)
	}
	_, w, err := pageTab(set.String("id", ""))
	if err != nil {
		return err
	}
	nodes, err := webTree(&w, pageMaxNodes, pageMaxDepth)
	if err != nil {
		return err
	}
	keep := func(e pageElement) bool { return role == "" || e.Role == role }
	what := "element"
	if role != "" {
		what = role
	}
	e, err := pickElement(nodes, ref, query, set.Has("exact"), nth, keep, what)
	if err != nil {
		return err
	}
	if err := actOn(w, e, engine.WebPress); err != nil {
		var exit *cli.ExitError
		if errors.As(err, &exit) || runtime.GOOS != "windows" {
			return err
		}
		// UI Automation: an element with no Invoke pattern is clicked at its
		// centre, which needs the window in front.
		if b := nodes[e.index].Bounds; b.W > 0 && b.H > 0 {
			if ferr := engine.FocusWindowID(w.ID); ferr != nil {
				return err
			}
			time.Sleep(150 * time.Millisecond)
			if cerr := engine.ClickAt(b.X+b.W/2, b.Y+b.H/2); cerr != nil {
				return cerr
			}
		} else {
			return err
		}
	}
	c.Printf("clicked %s  %s\n", e.Ref, e.annotation(false))
	return nil
}

func runWatchTab(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id", "match", "interval", "timeout", "max")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "match", "exact", "interval", "timeout", "max"); err != nil {
		return err
	}
	interval, max, err := watchOptions(set)
	if err != nil {
		return err
	}
	var timeout time.Duration
	if v := set.String("timeout", ""); v != "" {
		if timeout, err = parseInterval(v, 0); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	id := set.String("id", "")
	t, w, err := pageTab(id)
	if err != nil {
		return err
	}
	read := func() ([]string, error) {
		nodes, err := webTree(&w, pageMaxNodes, pageMaxDepth)
		if err != nil {
			return nil, err
		}
		return watchLines(nodes), nil
	}
	return watchTabLoop(c, tabWatch{
		tab: t.ID, match: set.String("match", ""), exact: set.Has("exact"),
		interval: interval, timeout: timeout, max: max, read: read,
		selected: func() (bool, error) {
			cur, err := pickTab(tabFilter{ID: id}, true)
			return err == nil && cur.Active, err
		},
	})
}

type tabWatch struct {
	tab      string
	match    string
	exact    bool
	interval time.Duration
	timeout  time.Duration
	max      int
	read     func() ([]string, error)
	selected func() (bool, error) // is the tab still the one its window shows
	now      func() time.Time
}

// watchTabLoop re-reads the page every interval and prints the lines that
// were not there before. The first read is the baseline, not news, so
// --match waits for text that appears after the watch starts. If someone
// switches the window to another tab, ticks are skipped (with a warning)
// rather than reporting the other page or switching back under them.
func watchTabLoop(c *cli.Ctx, o tabWatch) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if o.now == nil {
		o.now = time.Now
	}
	var deadline <-chan time.Time
	if o.timeout > 0 {
		timer := time.NewTimer(o.timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()
	var prev []string
	started, warned, seq := false, false, 0
	for {
		ok := true
		if started && o.selected != nil {
			sel, err := o.selected()
			if err != nil {
				return err
			}
			if ok = sel; !ok && !warned {
				c.Warnf("aos: %s is not the selected tab of its window any more; waiting for it to be selected again\n", o.tab)
				warned = true
			}
		}
		if ok {
			warned = false
			cur, err := o.read()
			if err != nil {
				c.Warnf("aos: %v\n", err)
			} else if !started {
				started, prev = true, cur
			} else {
				added := addedText(prev, cur)
				prev = cur
				if len(added) > 0 {
					seq++
					line, err := json.Marshal(watchEvent{Event: "tab", At: o.now().Format(time.RFC3339), Seq: seq, Tab: o.tab, Added: added})
					if err != nil {
						return err
					}
					c.Printf("%s\n", line)
					if o.match != "" {
						for _, a := range added {
							if textMatches(a, o.match, o.exact) {
								return nil
							}
						}
					} else if o.max > 0 && seq >= o.max {
						return nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			if o.match != "" {
				return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%q did not appear in %s within %s", o.match, o.tab, o.timeout)}
			}
			return nil
		case <-ticker.C:
		}
	}
}
