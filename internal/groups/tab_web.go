package groups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// Reading and driving the page inside a browser tab through accessibility:
// `tab read`, `tab type`, `tab click` and `watch tab`. The tab is selected in
// its window without raising the window. A click is an accessibility press;
// typing focuses the field through accessibility and posts key events to the
// browser's process, so the frontmost app does not change (the browser's own
// key window can: see focusGuard).

func init() {
	register(func(r *cli.Registry) {
		platforms := []string{"darwin", "windows"}
		r.Add(
			&cli.Command{
				Group: "tab", Name: "read",
				Summary: "Read a browser tab's page as markdown, with refs for its buttons, links and fields",
				Args:    "(--id=<tab id> | --url=<substring> | --title=<substring>) [--app=<name>] [--md | --json]",
				Examples: []string{
					"aos tab read --id=chrome:1658108662:1658109078",
					"aos tab read --url=web.whatsapp.com --json",
					"# [e7] Message (textarea, empty)   [e8] Send (button)",
				},
				Platforms: platforms,
				Run:       runTabRead,
			},
			&cli.Command{
				Group: "tab", Name: "type",
				Summary: "Type into a field of a browser tab's page without taking focus",
				Args:    "(--id=<tab id> | --url=<substring> | --title=<substring>) [--app=<name>] (--element=<ref> | --into=<label>) --text=<text> [--enter] [--exact] [--nth=<n>] [--allow-newline] [--allow-focus]",
				Examples: []string{
					`aos tab type --id=chrome:1658108662:1658109078 --element=e7 --text="hello" --enter`,
					`aos tab type --url=web.whatsapp.com --into="Type a message" --text="hello" --enter`,
					"# exit 3 when no field matches, the page changed or focus moved; 4 when several fields match",
				},
				Platforms: platforms,
				Run:       runTabType,
			},
			&cli.Command{
				Group: "tab", Name: "click",
				Summary: "Press a button or link in a browser tab's page, without moving the mouse",
				Args:    "(--id=<tab id> | --url=<substring> | --title=<substring>) [--app=<name>] (--element=<ref> | --find=<label>) [--role=button|link|checkbox|radio|...] [--exact] [--nth=<n>] [--allow-focus]",
				Examples: []string{
					"aos tab click --id=chrome:1658108662:1658109078 --element=e8",
					`aos tab click --title="Telegram" --find=Send --role=button`,
					"# exit 3 when nothing matches or the page changed, 4 when several elements match",
				},
				Platforms: platforms,
				Run:       runTabClick,
			},
			&cli.Command{
				Group: "watch", Name: "tab",
				Summary:  "Print a JSON line with the new text whenever a browser tab's page changes",
				Blocking: true,
				Args:     "(--id=<tab id> | --url=<substring> | --title=<substring>) [--app=<name>] [--match=<text>] [--exact] [--interval=<1s>] [--timeout=<duration>] [--max=<n>]",
				Examples: []string{
					"aos watch tab --id=chrome:1658108662:1658109078",
					`aos watch tab --url=web.whatsapp.com --match="Build finished" --timeout=5m`,
					"# with --match: exit 0 when the text appears, 3 on --timeout or when the tab closes",
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
	webTree     = engine.WebTree
	webAct      = engine.WebAct
	pageTab     = resolvePageTab
	typeInto    = engine.PostTextTo
	pressInto   = engine.PostKeyTo
	webPrepare  = engine.WebPrepare
	webFocus    = engine.FocusWebElement
	frontWindow = focusedWindow
	frontPID    = engine.FrontmostPID
	tabNow      = func(id string) (Tab, error) { return pickTab(tabFilter{ID: id}, true) }
)

// quietSelector is a provider that can select a tab in its window without
// raising the window.
type quietSelector interface {
	selectQuiet(ctx context.Context, t Tab) error
}

// pageFilter is the tab a page command names: --id, or a --url / --title
// substring (narrowed by --app). --exact belongs to the element or text
// match, so the tab match is always a substring; several matches exit 4.
func pageFilter(set *argSet) tabFilter {
	return tabFilter{
		App:   set.String("app", ""),
		Kind:  "browser",
		Title: set.String("title", ""),
		URL:   set.String("url", ""),
		ID:    set.String("id", ""),
	}
}

// resolvePageTab finds one browser tab, selects it in its window if it
// is not already the selected one, and returns its window.
func resolvePageTab(f tabFilter) (Tab, engine.Window, error) {
	if f.ID == "" && f.URL == "" && f.Title == "" {
		return Tab{}, engine.Window{}, fmt.Errorf("name a tab: --id (from `aos tab list --kind=browser`), --url or --title")
	}
	t, err := pickTab(f, true)
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
	set, err := parseArgs(args, "id", "url", "title", "app")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "url", "title", "app", "md", "json"); err != nil {
		return err
	}
	if set.Has("md") && set.Has("json") {
		return fmt.Errorf("--md and --json are exclusive")
	}
	t, w, err := pageTab(pageFilter(set))
	if err != nil {
		return err
	}
	restore, err := webPrepare(w)
	defer restore()
	if err != nil {
		return err
	}
	nodes, err := webTree(&w, pageMaxNodes, pageMaxDepth)
	if err != nil {
		return err
	}
	elems, err := pageRefs(t, nodes)
	if err != nil {
		c.Warnf("aos: refs not saved, so --element cannot use them: %v\n", err)
	}
	md := strings.Join(renderPageElems(nodes, elems, true, false), "\n")
	if set.Has("json") {
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
func pickElement(elems []pageElement, ref string, fp *refFP, query string, exact bool, nth int, keep func(pageElement) bool, what string) (pageElement, error) {
	if ref != "" && fp != nil {
		e, err := byFingerprint(elems, ref, *fp)
		if err == nil && keep != nil && !keep(e) {
			return pageElement{}, fmt.Errorf("%s is a %s, not a %s", ref, e.Role, what)
		}
		return e, err
	}
	if ref != "" {
		if _, err := parseElementRef(ref); err != nil {
			return pageElement{}, err
		}
		return pageElement{}, &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf(
			"%s is not a ref from this tab's last `aos tab read` (none yet, or the tab moved to another page); run `aos tab read` first", ref)}
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

// resolveAndAct reads the page, resolves the element and acts on it. The
// adapter refuses to act when the element at the resolved position is not
// the one resolved (a live page inserted something in between); then the
// page is read and resolved once more. A second miss is exit 3.
func resolveAndAct(w *engine.Window, pick func([]engine.WebNode) (pageElement, error), act func(pageElement) error) (pageElement, []engine.WebNode, error) {
	for attempt := 0; ; attempt++ {
		nodes, err := webTree(w, pageMaxNodes, pageMaxDepth)
		if err != nil {
			return pageElement{}, nil, err
		}
		e, err := pick(nodes)
		if err != nil {
			return e, nodes, err
		}
		err = act(e)
		if errors.Is(err, engine.ErrWebStale) && attempt == 0 {
			continue
		}
		return e, nodes, actErr(e, err)
	}
}

func actErr(e pageElement, err error) error {
	if errors.Is(err, engine.ErrWebStale) {
		return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%s is no longer %q (%s): the page changed; run `aos tab read` again", e.Ref, e.Label, e.Role)}
	}
	if errors.Is(err, engine.ErrWebFocus) {
		return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("focus did not land on %s %q; nothing typed", e.Ref, e.Label)}
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

// typeBurst is how many characters go out between focus checks: small
// enough that a focus change costs at most this many stray characters.
const typeBurst = 16

// focusGuard refuses to make another window the browser's key window
// while the person is using that browser.
// It fails closed: when the frontmost window cannot be read, nothing is typed.
// It runs before focusing and again before every burst, because the person
// can switch to the browser mid-typing.
func focusGuard(w engine.Window, allow bool) error {
	if allow {
		return nil
	}
	// The frontmost app first: when it is not the browser, keys the person
	// types go elsewhere and making a browser window key cannot steal them.
	pid, err := frontPID()
	if err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("cannot tell which app is in front (%v); not typing (pass --allow-focus to type anyway)", err)}
	}
	if pid != w.PID {
		return nil
	}
	// The browser is in front: only its window holding the tab is safe.
	front, err := frontWindow()
	if err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%s is in front and its focused window cannot be read (%v); not typing (pass --allow-focus to type anyway)", w.App, err)}
	}
	if front.ID == w.ID {
		return nil
	}
	return &cli.ExitError{Code: 1, Message: fmt.Sprintf(
		"%s is the app in front (window %q) and the tab is in another of its windows; typing would make that window %s's key window under you. Retry when %s is not in front, or pass --allow-focus",
		w.App, front.Title, w.App, w.App)}
}

// sameTab checks the tab is still the selected tab of its window and still
// shows the page that was resolved.
func sameTab(t Tab) error {
	cur, err := tabNow(t.ID)
	if err != nil {
		return err
	}
	if !cur.Active || cur.URL != t.URL || cur.Title != t.Title {
		return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf(
			"%s changed (now %q %s, selected=%v); nothing typed. Run `aos tab read` again", t.ID, cur.Title, cur.URL, cur.Active)}
	}
	return nil
}

// stillFocused checks, before a burst, that the browser's focused element is
// still the very element that was focused (CFEqual / CompareElements, so a
// second box with the same label does not pass) and that the person has not
// switched to the browser meanwhile.
func stillFocused(w engine.Window, target engine.WebTarget, e pageElement, allow bool, sent, total int) error {
	if err := focusGuard(w, allow); err != nil {
		if sent == 0 {
			return err
		}
		return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%v; stopped after %d of %d characters", err, sent, total)}
	}
	if ok, err := target.StillFocused(); err == nil && ok {
		return nil
	}
	return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf(
		"focus left %s %q after %d of %d characters; stopped", e.Ref, e.Label, sent, total)}
}

func runTabType(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id", "url", "title", "app", "element", "into", "text", "nth")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "url", "title", "app", "element", "into", "text", "nth", "enter", "exact", "allow-focus", "allow-newline"); err != nil {
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
	// A newline is a Return key: in a chat box it sends half a message.
	if strings.ContainsAny(text, "\r\n") && !set.Has("allow-newline") {
		return fmt.Errorf("--text contains a newline, which is typed as Return (it can submit the form); pass --allow-newline if you mean it, or use --enter at the end")
	}
	t, w, err := pageTab(pageFilter(set))
	if err != nil {
		return err
	}
	restore, err := webPrepare(w)
	defer restore()
	if err != nil {
		return err
	}
	allow := set.Has("allow-focus")
	if err := focusGuard(w, allow); err != nil {
		return err
	}
	fp := loadRef(t, ref)
	var target engine.WebTarget
	e, _, err := resolveAndAct(&w, func(nodes []engine.WebNode) (pageElement, error) {
		elems, _ := pageRefs(t, nodes)
		return pickElement(elems, ref, fp, query, set.Has("exact"), nth, func(e pageElement) bool { return pageEditable[e.Role] }, "field")
	}, func(e pageElement) (err error) {
		target, err = webFocus(w, pageMaxNodes, pageMaxDepth, e.index, e.axRole, e.fp)
		return err
	})
	if err != nil {
		return err
	}
	defer target.Release()
	if err := sameTab(t); err != nil {
		return err
	}
	// Pages ignore values set through accessibility (React, Lexical keep
	// their own state), so the text goes in as key events posted to the
	// browser's process, in bursts, with the field's focus re-checked before
	// each burst and before Enter.
	runes := []rune(text)
	for i := 0; i < len(runes); i += typeBurst {
		if err := stillFocused(w, target, e, allow, i, len(runes)); err != nil {
			return err
		}
		end := min(i+typeBurst, len(runes))
		if err := typeInto(w, string(runes[i:end])); err != nil {
			return err
		}
	}
	if set.Has("enter") {
		if err := stillFocused(w, target, e, allow, len(runes), len(runes)); err != nil {
			return err
		}
		if err := pressInto(w, "enter"); err != nil {
			return err
		}
	}
	c.Printf("typed into %s  %s\n", e.Ref, e.annotation(false))
	return nil
}

func runTabClick(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id", "url", "title", "app", "element", "find", "role", "nth")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "url", "title", "app", "element", "find", "role", "nth", "exact", "allow-focus"); err != nil {
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
	t, w, err := pageTab(pageFilter(set))
	if err != nil {
		return err
	}
	restore, err := webPrepare(w)
	defer restore()
	if err != nil {
		return err
	}
	keep := func(e pageElement) bool { return role == "" || e.Role == role }
	what := "element"
	if role != "" {
		what = role
	}
	fp := loadRef(t, ref)
	e, nodes, err := resolveAndAct(&w, func(nodes []engine.WebNode) (pageElement, error) {
		elems, _ := pageRefs(t, nodes)
		return pickElement(elems, ref, fp, query, set.Has("exact"), nth, keep, what)
	}, func(e pageElement) error {
		return webAct(w, pageMaxNodes, pageMaxDepth, e.index, engine.WebPress, e.axRole, e.fp)
	})
	if err != nil {
		var exit *cli.ExitError
		if errors.As(err, &exit) || runtime.GOOS != "windows" {
			return err
		}
		// UI Automation: an element with no Invoke pattern can only be
		// clicked, which raises the window and moves the mouse.
		b := nodes[e.index].Bounds
		if !set.Has("allow-focus") || b.W <= 0 || b.H <= 0 {
			return fmt.Errorf("%s has no Invoke pattern (%v); pass --allow-focus to raise the window and click its centre", e.Ref, err)
		}
		if ferr := engine.FocusWindowID(w.ID); ferr != nil {
			return ferr
		}
		time.Sleep(150 * time.Millisecond)
		if cerr := engine.ClickAt(b.X+b.W/2, b.Y+b.H/2); cerr != nil {
			return cerr
		}
	}
	c.Printf("clicked %s  %s\n", e.Ref, e.annotation(false))
	return nil
}

// minTabWatchInterval: each read is a full accessibility walk of the page.
const minTabWatchInterval = time.Second

func runWatchTab(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id", "url", "title", "app", "match", "interval", "timeout", "max")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "url", "title", "app", "match", "exact", "interval", "timeout", "max"); err != nil {
		return err
	}
	interval, max, err := watchOptions(set)
	if err != nil {
		return err
	}
	if !set.Has("interval") {
		interval = minTabWatchInterval
	}
	if interval < minTabWatchInterval {
		return fmt.Errorf("--interval for watch tab must be at least %s", minTabWatchInterval)
	}
	var timeout time.Duration
	if v := set.String("timeout", ""); v != "" {
		if timeout, err = parseInterval(v, 0); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	t, w, err := pageTab(pageFilter(set))
	if err != nil {
		return err
	}
	id := t.ID
	// AXEnhancedUserInterface stays on for the watch and is restored when it
	// ends (Ctrl-C included: the loop returns rather than being killed).
	restore, err := webPrepare(w)
	defer restore()
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
			cur, err := tabNow(id)
			if err != nil {
				return false, &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%s closed: %v", id, err)}
			}
			return cur.Active, nil
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
	// SIGTERM / SIGHUP too: agents and timeouts end watches that way, and the
	// deferred AXEnhancedUserInterface restore must still run.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
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

// refFP is what a ref from `tab read` stands for: the element's identity,
// not its position. Live pages (chats, feeds) insert elements above a field
// between the read and the action, so a position goes stale in seconds.
type refFP struct {
	Role  string `json:"role"`             // raw accessibility role
	Label string `json:"label"`            // engine.WebNode.WebLabel()
	DOMID string `json:"dom_id,omitempty"` // HTML id: the strongest identity when present
	Ctx   string `json:"ctx,omitempty"`    // nearest text before it
	Box   string `json:"box,omitempty"`    // bounds rounded to 10 px
}

type refCache struct {
	Tab  string           `json:"tab"`
	URL  string           `json:"url"`
	Refs map[string]refFP `json:"refs"`
}

// refCacheDir is where the last read's refs are kept per tab (a seam for tests).
var refCacheDir = func() (string, error) {
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "aos", "tab-refs"), nil
}

func refCachePath(tabID string) (string, error) {
	d, err := refCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(tabID))
	return filepath.Join(d, hex.EncodeToString(sum[:8])+".json"), nil
}

func fingerprintOf(e pageElement) refFP {
	return refFP{Role: e.axRole, Label: e.fp, DOMID: e.domID, Ctx: e.ctx, Box: e.box}
}

type refCacheFile struct {
	refCache
	Next int `json:"next"`
}

func loadCache(t Tab) refCacheFile {
	fresh := refCacheFile{refCache: refCache{Tab: t.ID, URL: t.URL, Refs: map[string]refFP{}}}
	p, err := refCachePath(t.ID)
	if err != nil {
		return fresh
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return fresh
	}
	var rc refCacheFile
	if json.Unmarshal(b, &rc) != nil || rc.Tab != t.ID || rc.URL != t.URL || rc.Refs == nil {
		return fresh
	}
	return rc
}

func fpKey(f refFP) string {
	return f.Role + "\x00" + f.Label + "\x00" + f.DOMID
}

// carryRef picks which cached ref (of those with f's role, label and id,
// not yet given out) an element keeps. alone: it is the only such element
// now and there was one before, so it is the same element.
func carryRef(cached map[string]refFP, cands []string, taken map[string]refFP, f refFP, alone bool) (string, bool) {
	var free []string
	for _, r := range cands {
		if _, used := taken[r]; !used {
			free = append(free, r)
		}
	}
	if alone && len(cands) == 1 && len(free) == 1 {
		return free[0], true
	}
	for _, match := range []func(refFP) bool{
		func(c refFP) bool { return c.Ctx == f.Ctx },
		func(c refFP) bool { return c.Ctx == f.Ctx && c.Box == f.Box },
	} {
		var hit []string
		for _, r := range free {
			if match(cached[r]) {
				hit = append(hit, r)
			}
		}
		if len(hit) == 1 {
			return hit[0], true
		}
	}
	return "", false
}

// pageRefs gives the page's elements refs that stay the same across reads:
// an element whose fingerprint was in the tab's last read keeps its ref; a
// new element gets the next number (never reused on this page); elements
// that are gone are dropped. The result is saved for --element.
func pageRefs(t Tab, nodes []engine.WebNode) ([]pageElement, error) {
	elems := pageElements(nodes)
	rc := loadCache(t)
	// Cached refs by role+label+id. A unique one carries over as is; among
	// several, the one with the same text before it, else the same bounds.
	groups := map[string][]string{}
	for ref, f := range rc.Refs {
		groups[fpKey(f)] = append(groups[fpKey(f)], ref)
	}
	now := map[string]int{}
	for _, e := range elems {
		now[fpKey(fingerprintOf(e))]++
	}
	refs := map[string]refFP{}
	// A new element is numbered by its position when that number was never
	// issued on this page (so a first read reads like the tree), else by
	// the next free number. Next is a high-water mark: a ref is never
	// reissued to a different element.
	high := rc.Next
	for i := range elems {
		f := fingerprintOf(elems[i])
		ref, ok := carryRef(rc.Refs, groups[fpKey(f)], refs, f, now[fpKey(f)] == 1)
		if !ok {
			n := elems[i].index
			if n < high {
				n = rc.Next
			}
			ref = elementRef(n)
			if n+1 > rc.Next {
				rc.Next = n + 1
			}
		}
		elems[i].Ref = ref
		refs[ref] = f
	}
	rc.Refs = refs
	p, err := refCachePath(t.ID)
	if err != nil {
		return elems, err
	}
	b, err := json.Marshal(rc)
	if err != nil {
		return elems, err
	}
	return elems, writeAtomic(p, b)
}

// writeAtomic writes through a temp file in the same folder and a rename,
// so a concurrent read (another aos command) never sees half a file.
func writeAtomic(p string, b []byte) error {
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".refs-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// loadRef returns the fingerprint a ref stood for in the tab's last read, or
// nil when there is none for this tab and URL (then the ref is a position).
func loadRef(t Tab, ref string) *refFP {
	if ref == "" {
		return nil
	}
	p, err := refCachePath(t.ID)
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var rc refCache
	if json.Unmarshal(b, &rc) != nil || rc.Tab != t.ID || rc.URL != t.URL {
		return nil
	}
	fp, ok := rc.Refs[strings.TrimSpace(ref)]
	if !ok {
		return nil
	}
	return &fp
}

// byFingerprint finds the element a ref stood for on the page as it is now.
// Candidates share its role, label and HTML id. One candidate is it. Several
// are narrowed by the text before them (which message a "Reply" belongs
// to), then by rounded bounds. Never by position among them: in a list
// where one item went and another came, that lands on a neighbour. Still
// several: exit 4 with the candidates. None: exit 3.
func byFingerprint(elems []pageElement, ref string, fp refFP) (pageElement, error) {
	var same []pageElement
	for _, e := range elems {
		if e.axRole == fp.Role && e.fp == fp.Label && e.domID == fp.DOMID {
			same = append(same, e)
		}
	}
	if len(same) == 0 {
		return pageElement{}, &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf(
			"%s (%q) is not on the page any more; run `aos tab read` again", ref, fp.Label)}
	}
	if len(same) == 1 {
		return same[0], nil
	}
	narrow := func(in []pageElement, ok func(pageElement) bool) []pageElement {
		var out []pageElement
		for _, e := range in {
			if ok(e) {
				out = append(out, e)
			}
		}
		return out
	}
	byCtx := narrow(same, func(e pageElement) bool { return e.ctx == fp.Ctx })
	if len(byCtx) == 1 {
		return byCtx[0], nil
	}
	pool := byCtx
	if len(pool) == 0 {
		pool = same
	}
	if byBox := narrow(pool, func(e pageElement) bool { return e.box == fp.Box }); len(byBox) == 1 && len(byCtx) > 0 {
		return byBox[0], nil
	}
	msg := fmt.Sprintf("%s (%q) could be any of %d elements now; name one with its current ref:", ref, fp.Label, len(pool))
	for _, e := range pool {
		msg += "\n  " + e.annotation(true)
		if e.ctx != "" {
			msg += fmt.Sprintf("  after %q", e.ctx)
		}
	}
	return pageElement{}, &cli.ExitError{Code: exitAmbiguous, Message: msg}
}
