package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// A tab is the unit people actually switch between: a browser page, a
// terminal session. Windows know nothing about them, so each app family is
// asked through whatever it speaks — AppleScript/JXA on macOS, UI Automation
// on Windows — with an accessibility walk as the catch-all. A browser or a
// terminal is just a window kind; see windowKind.

func init() {
	register(func(r *cli.Registry) {
		r.Describe("tab", "Browser and terminal tabs: list, focus, open, send, close")
		platforms := []string{"darwin", "windows"}
		r.Add(
			&cli.Command{
				Group: "tab", Name: "list",
				Summary: "List the tabs of every browser and terminal window",
				Args:    "[--app=<name>] [--kind=browser|terminal|editor|files|other] [--window=<aos window id>] [--json]",
				Examples: []string{
					"aos tab list",
					"aos tab list --kind=browser --json",
					"aos tab list --app=Ghostty",
				},
				Platforms: platforms,
				Run:       runTabList,
			},
			&cli.Command{
				Group: "tab", Name: "focus",
				Summary: "Raise a tab's window and select the tab",
				Args:    "(--title=<substring> | --url=<substring> | --cwd=<substring> | --id=<tab id>) [--app=<name>] [--kind=<kind>] [--exact]",
				Examples: []string{
					"aos tab focus --url=github.com/muthuishere/aos/pulls",
					"aos tab focus --title=Inbox --app=Chrome",
					"aos tab focus --cwd=os-cli --kind=terminal",
					"# exit 3 when no tab matches",
				},
				Platforms: platforms,
				Run:       runTabFocus,
			},
			&cli.Command{
				Group: "tab", Name: "open",
				Summary: "Open a browser tab on a URL, or a terminal tab in a folder",
				Args:    "[<url>] --app=<name> [--window=<id> | --new-window] [--cwd=<dir>] [--command=<cmd>] [--background]",
				Examples: []string{
					"aos tab open https://example.com --app=Chrome",
					"aos tab open https://example.com --app=Safari --new-window",
					`aos tab open --app=Ghostty --cwd=~/src/aos --command="task check"`,
				},
				Platforms: platforms,
				Run:       runTabOpen,
			},
			&cli.Command{
				Group: "tab", Name: "send",
				Summary: "Type text or press a key in a terminal tab without focusing it",
				Args:    "--id=<tab id> (--text=<text> | --key=<combo>)",
				Examples: []string{
					`aos tab send --id=ghostty:tab-group-8039faee0:tab-802a71200 --text="ls -la"`,
					"aos tab send --id=ghostty:tab-group-8039faee0:tab-802a71200 --key=enter",
					"aos tab send --id=iterm:31:1 --key=ctrl+c",
				},
				Platforms: platforms,
				Run:       runTabSend,
			},
			&cli.Command{
				Group: "tab", Name: "close",
				Summary: "Close one tab (never the whole window)",
				Args:    "(--id=<tab id> | --title=<substring> | --url=<substring>) [--app=<name>] [--kind=<kind>] [--exact]",
				Examples: []string{
					"aos tab close --id=chrome:1658108662:1658109078",
					"aos tab close --title=example --app=Chrome",
				},
				Platforms: platforms,
				Run:       runTabClose,
			},
		)
	})
}

// Tab is one tab as `tab list` reports it.
type Tab struct {
	ID           string `json:"id"`
	App          string `json:"app"`
	Kind         string `json:"kind"`
	Window       string `json:"window,omitempty"`        // the aos window ID, when it could be matched
	NativeWindow string `json:"native_window,omitempty"` // the app's own window id
	Index        int    `json:"index"`                   // 1-based, left to right
	Title        string `json:"title"`
	URL          string `json:"url,omitempty"`
	CWD          string `json:"cwd,omitempty"`
	Active       bool   `json:"active"` // the selected tab of its window
	Source       string `json:"source"` // applescript, ax, uia, cli
	Panes        []Pane `json:"panes,omitempty"`

	// For matching the app's window onto an aos window; not printed.
	windowTitle  string
	windowBounds *engine.Rect
	minimized    bool
	center       *engine.Point // the tab's on-screen centre, for accessibility tabs
}

// Pane is one split inside a terminal tab.
type Pane struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	CWD    string `json:"cwd,omitempty"`
	Active bool   `json:"active"`
}

// tabProvider is one way of reaching an app family's tabs.
type tabProvider interface {
	// owns reports whether a tab-id prefix ("chrome", "ghostty") is this
	// provider's.
	owns(slug string) bool
	// handles reports whether the provider drives this app name.
	handles(app string) bool
	// list returns the tabs of every running app it handles that the filter
	// allows; apps lists the on-screen apps the filter allows. Apps that are
	// not running are skipped, never launched.
	list(ctx context.Context, f tabFilter, apps []string) ([]Tab, error)
	focus(ctx context.Context, t Tab) error
	open(ctx context.Context, req tabOpenRequest) (string, error)
	send(ctx context.Context, t Tab, text, key string) error
	close(ctx context.Context, t Tab) error
}

type tabOpenRequest struct {
	App        string
	URL        string
	Window     string // native window id, resolved from an aos id or a tab id
	NewWindow  bool
	CWD        string
	Command    string
	Background bool
}

// providerTimeout bounds each provider's list call so one hung app (a
// beachballing browser, a pending permission prompt) cannot stall the rest.
var providerTimeout = 1500 * time.Millisecond

// errTabUnsupported explains an action an app family cannot do.
type errTabUnsupported struct{ what string }

func (e errTabUnsupported) Error() string { return e.what }

// tabProviders is set by the platform file.
var tabProviders func() []tabProvider

// windowKinds maps an app name fragment onto a window kind, checked in order.
var windowKinds = []struct{ frag, kind string }{
	{"ghostty", "terminal"}, {"iterm", "terminal"}, {"windowsterminal", "terminal"},
	{"windows terminal", "terminal"}, {"wezterm", "terminal"}, {"alacritty", "terminal"},
	{"kitty", "terminal"}, {"warp", "terminal"}, {"konsole", "terminal"}, {"terminal", "terminal"},
	{"xterm", "terminal"}, {"hyper", "terminal"}, {"tabby", "terminal"}, {"conhost", "terminal"},
	{"chrome", "browser"}, {"chromium", "browser"}, {"msedge", "browser"}, {"microsoft edge", "browser"},
	{"brave", "browser"}, {"arc", "browser"}, {"vivaldi", "browser"}, {"safari", "browser"},
	{"firefox", "browser"}, {"opera", "browser"}, {"orion", "browser"}, {"zen", "browser"},
	{"code", "editor"}, {"cursor", "editor"}, {"zed", "editor"}, {"sublime", "editor"},
	{"intellij", "editor"}, {"idea", "editor"}, {"goland", "editor"}, {"pycharm", "editor"},
	{"webstorm", "editor"}, {"notepad", "editor"}, {"textedit", "editor"}, {"windsurf", "editor"},
	{"bbedit", "editor"}, {"emacs", "editor"}, {"vim", "editor"},
	{"finder", "files"}, {"explorer", "files"}, {"nautilus", "files"}, {"dolphin", "files"},
	{"thunar", "files"}, {"nemo", "files"},
}

// windowKind classifies a window by its app: browser, terminal, editor,
// files, or other.
func windowKind(app string) string {
	a := strings.ToLower(strings.TrimSuffix(app, ".exe"))
	for _, k := range windowKinds {
		if a == k.frag || strings.Contains(a, k.frag) {
			return k.kind
		}
	}
	return "other"
}

var tabKindNames = []string{"browser", "terminal", "editor", "files", "other"}

func checkKind(kind string) error {
	if kind == "" {
		return nil
	}
	for _, k := range tabKindNames {
		if k == kind {
			return nil
		}
	}
	return fmt.Errorf("--kind must be one of %s, got %q", strings.Join(tabKindNames, ", "), kind)
}

// appMatches is the loose app comparison every tab flag uses: equal ignoring
// case, or a substring ("chrome" finds "Google Chrome").
func appMatches(app, query string) bool {
	if query == "" {
		return true
	}
	a, q := strings.ToLower(app), strings.ToLower(strings.TrimSpace(query))
	return a == q || strings.Contains(a, q) || strings.TrimSuffix(a, ".exe") == q
}

// tabFilter narrows a tab list.
type tabFilter struct {
	App, Kind, Window string
	Title, URL, CWD   string
	ID                string
	Exact             bool
}

func (f tabFilter) selects(t Tab) bool {
	if !appMatches(t.App, f.App) || (f.Kind != "" && t.Kind != f.Kind) {
		return false
	}
	if f.Window != "" && t.Window != f.Window && t.NativeWindow != f.Window {
		return false
	}
	if f.ID != "" && t.ID != f.ID && !paneOf(t, f.ID) {
		return false
	}
	if f.Title != "" && !textMatches(t.Title, f.Title, f.Exact) {
		return false
	}
	if f.URL != "" && !textMatches(t.URL, f.URL, f.Exact) {
		return false
	}
	if f.CWD != "" && !cwdMatches(t, f.CWD, f.Exact) {
		return false
	}
	return true
}

func paneOf(t Tab, id string) bool {
	for _, p := range t.Panes {
		if p.ID == id {
			return true
		}
	}
	return false
}

func cwdMatches(t Tab, q string, exact bool) bool {
	if textMatches(t.CWD, q, exact) {
		return true
	}
	for _, p := range t.Panes {
		if textMatches(p.CWD, q, exact) {
			return true
		}
	}
	return false
}

func textMatches(have, want string, exact bool) bool {
	if have == "" {
		return false
	}
	if exact {
		return strings.EqualFold(have, want)
	}
	return strings.Contains(strings.ToLower(have), strings.ToLower(want))
}

// splitTabID breaks "chrome:<win>:<tab>[:<pane>]" into its prefix and parts.
// Native ids never contain a colon, except Windows/AX ids which are numeric.
func splitTabID(id string) (slug string, parts []string, err error) {
	fields := strings.Split(id, ":")
	if len(fields) < 3 || fields[0] == "" || fields[1] == "" || fields[2] == "" {
		return "", nil, fmt.Errorf("bad tab id %q: expected <app>:<window>:<tab>, as `aos tab list` prints it", id)
	}
	return fields[0], fields[1:], nil
}

// appSlug turns an app name into its id prefix: "Google Chrome" -> "chrome".
func appSlug(app string) string {
	a := strings.ToLower(strings.TrimSuffix(app, ".exe"))
	switch {
	case strings.Contains(a, "chrome canary"):
		return "canary"
	case strings.Contains(a, "chrome"):
		return "chrome"
	case strings.Contains(a, "edge") || a == "msedge":
		return "edge"
	case strings.Contains(a, "brave"):
		return "brave"
	case strings.Contains(a, "vivaldi"):
		return "vivaldi"
	case strings.Contains(a, "chromium"):
		return "chromium"
	case strings.Contains(a, "iterm"):
		return "iterm"
	}
	var b strings.Builder
	for _, r := range a {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// collectTabs runs every provider in parallel, each bounded by
// providerTimeout, and stamps kinds and aos window ids. Provider errors are
// returned alongside whatever the others found, so one app's denied
// permission does not hide every other app's tabs.
func collectTabs(f tabFilter, windows []engine.Window) ([]Tab, []error) {
	providers := tabProviders()
	var apps []string
	seen := map[string]bool{}
	for _, w := range windows {
		if !seen[w.App] {
			seen[w.App] = true
			apps = append(apps, w.App)
		}
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		tabs []Tab
		errs []error
	)
	for _, p := range providers {
		wg.Add(1)
		go func(p tabProvider) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), providerTimeout)
			defer cancel()
			got, err := p.list(ctx, f, appsFor(apps, f))
			mu.Lock()
			defer mu.Unlock()
			tabs = append(tabs, got...)
			if err != nil {
				errs = append(errs, err)
			}
		}(p)
	}
	wg.Wait()
	for i := range tabs {
		if tabs[i].Kind == "" {
			tabs[i].Kind = windowKind(tabs[i].App)
		}
	}
	stampWindows(tabs, windows)
	var out []Tab
	for _, t := range tabs {
		if f.selects(t) {
			out = append(out, t)
		}
	}
	sortTabs(out)
	return out, errs
}

// appsFor passes a provider the on-screen apps the filter allows. Scripted
// providers ignore it and check their own apps' running state.
func appsFor(apps []string, f tabFilter) []string {
	var out []string
	for _, a := range apps {
		kind := windowKind(a)
		// Unfiltered, only windows that usually have tabs are walked: an
		// accessibility walk of every window on screen is seconds of work.
		if f.App == "" && f.Kind == "" && kind == "other" {
			continue
		}
		if appMatches(a, f.App) && (f.Kind == "" || kind == f.Kind) {
			out = append(out, a)
		}
	}
	return out
}

func sortTabs(tabs []Tab) {
	sort.SliceStable(tabs, func(i, j int) bool {
		a, b := tabs[i], tabs[j]
		if a.App != b.App {
			return a.App < b.App
		}
		if a.NativeWindow != b.NativeWindow {
			return a.NativeWindow < b.NativeWindow
		}
		return a.Index < b.Index
	})
}

// stampWindows fills Window with the aos window ID of each tab's window. The
// app's own window id means nothing to the window list, so windows are paired
// by app, then by bounds and title (a browser window's title is its active
// tab's title). Each aos window is claimed once.
func stampWindows(tabs []Tab, windows []engine.Window) {
	type key struct{ app, native string }
	groups := map[key][]int{}
	var order []key
	for i, t := range tabs {
		if t.Window != "" {
			continue
		}
		k := key{t.App, t.NativeWindow}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], i)
	}
	claimed := map[string]bool{}
	for _, t := range tabs {
		if t.Window != "" {
			claimed[t.Window] = true
		}
	}
	for _, k := range order {
		first := tabs[groups[k][0]]
		if first.minimized {
			continue
		}
		best, bestScore := "", 0
		for _, w := range windows {
			if claimed[w.ID] || !strings.EqualFold(w.App, first.App) {
				continue
			}
			score := 0
			if b := first.windowBounds; b != nil && *b == w.Bounds {
				score += 2
			}
			if first.windowTitle != "" && w.Title == first.windowTitle {
				score++
			}
			if score > bestScore {
				best, bestScore = w.ID, score
			}
		}
		if best == "" {
			continue
		}
		claimed[best] = true
		for _, i := range groups[k] {
			tabs[i].Window = best
		}
	}
}

// listTabsFlags parses the flags shared by list / focus / close.
func tabFilterFrom(set *argSet) (tabFilter, error) {
	f := tabFilter{
		App:    set.String("app", ""),
		Kind:   strings.ToLower(set.String("kind", "")),
		Window: set.String("window", ""),
		Title:  set.String("title", ""),
		URL:    set.String("url", ""),
		CWD:    set.String("cwd", ""),
		ID:     set.String("id", ""),
		Exact:  set.Has("exact"),
	}
	if f.App == "" && len(set.Rest) > 0 {
		f.App = strings.Join(set.Rest, " ")
	}
	return f, checkKind(f.Kind)
}

// tabWindows is the on-screen window list tabs are matched against. Tests
// replace it.
var tabWindows = func() ([]engine.Window, error) { return engine.ListWindows(engine.Filter{}) }

func listTabs(f tabFilter) ([]Tab, []error, error) {
	windows, err := tabWindows()
	if err != nil {
		return nil, nil, err
	}
	tabs, errs := collectTabs(f, windows)
	return tabs, errs, nil
}

// warnTabErrors prints provider failures to stderr: the list still stands.
func warnTabErrors(c *cli.Ctx, errs []error) {
	for _, err := range errs {
		fmt.Fprintf(c.Stderr, "aos tab: %v\n", err)
	}
}

func runTabList(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "kind", "window")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "kind", "window", "json"); err != nil {
		return err
	}
	f, err := tabFilterFrom(set)
	if err != nil {
		return err
	}
	tabs, errs, err := listTabs(f)
	if err != nil {
		return err
	}
	warnTabErrors(c, errs)
	if set.Has("json") {
		if tabs == nil {
			tabs = []Tab{}
		}
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(tabs)
	}
	if len(tabs) == 0 {
		c.Println("no tabs matched")
		return &cli.ExitError{Code: 1}
	}
	printTabs(c, tabs)
	return nil
}

func printTabs(c *cli.Ctx, tabs []Tab) {
	width := 0
	for _, t := range tabs {
		if len(t.ID) > width {
			width = len(t.ID)
		}
	}
	for _, t := range tabs {
		mark := " "
		if t.Active {
			mark = "*"
		}
		where := t.URL
		if where == "" {
			where = t.CWD
		}
		line := fmt.Sprintf("%s %-*s  %-8s  %s", mark, width, t.ID, t.Kind, t.Title)
		if where != "" {
			line += "  " + where
		}
		c.Println(line)
		for _, p := range t.Panes {
			pm := " "
			if p.Active {
				pm = "*"
			}
			c.Printf("    %s %s  %s  %s\n", pm, p.ID, p.Title, p.CWD)
		}
	}
}

// pickTab resolves focus/close flags to exactly one tab: the active one
// among several matches, else the first. Exit 3 when nothing matches.
func pickTab(f tabFilter) (Tab, error) {
	if f.Title == "" && f.URL == "" && f.CWD == "" && f.ID == "" {
		return Tab{}, fmt.Errorf("name a tab: --id, --title, --url or --cwd")
	}
	if f.ID != "" {
		if _, _, err := splitTabID(f.ID); err != nil {
			return Tab{}, err
		}
	}
	tabs, errs, err := listTabs(f)
	if err != nil {
		return Tab{}, err
	}
	if len(tabs) == 0 {
		msg := "no tab matched " + describeTabFilter(f)
		for _, e := range errs {
			msg += "\n  " + e.Error()
		}
		return Tab{}, &cli.ExitError{Code: exitNotFound, Message: msg}
	}
	return bestTab(tabs), nil
}

func bestTab(tabs []Tab) Tab {
	for _, t := range tabs {
		if t.Active {
			return t
		}
	}
	return tabs[0]
}

func describeTabFilter(f tabFilter) string {
	var parts []string
	add := func(k, v string) {
		if v != "" {
			parts = append(parts, fmt.Sprintf("%s=%q", k, v))
		}
	}
	add("id", f.ID)
	add("title", f.Title)
	add("url", f.URL)
	add("cwd", f.CWD)
	add("app", f.App)
	add("kind", f.Kind)
	return strings.Join(parts, " ")
}

// providerFor finds the provider that owns a tab.
func providerFor(t Tab) (tabProvider, error) {
	slug, _, err := splitTabID(t.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range tabProviders() {
		if p.owns(slug) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no tab provider for %s", t.ID)
}

func tabCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 8*time.Second)
}

func runTabFocus(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "kind", "title", "url", "cwd", "id")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "kind", "title", "url", "cwd", "id", "exact", "json"); err != nil {
		return err
	}
	f, err := tabFilterFrom(set)
	if err != nil {
		return err
	}
	t, err := pickTab(f)
	if err != nil {
		return err
	}
	p, err := providerFor(t)
	if err != nil {
		return err
	}
	if f.ID != "" && f.ID != t.ID {
		t = withPane(t, f.ID)
	}
	ctx, cancel := tabCtx()
	defer cancel()
	if err := p.focus(ctx, t); err != nil {
		return err
	}
	return printOneTab(c, set, t)
}

// withPane narrows a tab to one pane, so focus/send target that split.
func withPane(t Tab, paneID string) Tab {
	for i := range t.Panes {
		t.Panes[i].Active = t.Panes[i].ID == paneID
	}
	return t
}

func printOneTab(c *cli.Ctx, set *argSet, t Tab) error {
	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(t)
	}
	c.Printf("%s  %s\n", t.ID, t.Title)
	return nil
}

func runTabOpen(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "window", "cwd", "command")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "window", "new-window", "cwd", "command", "background"); err != nil {
		return err
	}
	req := tabOpenRequest{
		App:        set.String("app", ""),
		NewWindow:  set.Has("new-window"),
		CWD:        expandHome(set.String("cwd", "")),
		Command:    set.String("command", ""),
		Background: set.Has("background"),
	}
	if len(set.Rest) > 1 {
		return fmt.Errorf("open takes one URL, got %d words; quote it", len(set.Rest))
	}
	if len(set.Rest) == 1 {
		req.URL = set.Rest[0]
	}
	if req.App == "" {
		return fmt.Errorf("--app is required: the browser or terminal to open the tab in")
	}
	if req.NewWindow && set.Has("window") {
		return fmt.Errorf("--window and --new-window are exclusive")
	}
	var provider tabProvider
	for _, p := range tabProviders() {
		if p.handles(req.App) {
			provider = p
			break
		}
	}
	if provider == nil {
		return fmt.Errorf("aos cannot open tabs in %q; it knows %s", req.App, knownTabApps())
	}
	if w := set.String("window", ""); w != "" {
		native, err := nativeWindowFor(w, req.App)
		if err != nil {
			return err
		}
		req.Window = native
	}
	ctx, cancel := tabCtx()
	defer cancel()
	id, err := provider.open(ctx, req)
	if err != nil {
		return err
	}
	c.Println(id)
	return nil
}

// nativeWindowFor accepts an aos window id, an app window id, or a tab id
// and returns the app's own window id.
func nativeWindowFor(w, app string) (string, error) {
	if strings.Contains(w, ":") {
		_, parts, err := splitTabID(w)
		if err != nil {
			return "", err
		}
		return parts[0], nil
	}
	tabs, _, err := listTabs(tabFilter{App: app})
	if err != nil {
		return "", err
	}
	for _, t := range tabs {
		if t.Window == w || t.NativeWindow == w {
			return t.NativeWindow, nil
		}
	}
	return "", &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("no %s window with id %s (see `aos tab list --app=%q`)", app, w, app)}
}

func knownTabApps() string {
	var names []string
	for _, p := range tabProviders() {
		if n, ok := p.(interface{ appNames() []string }); ok {
			names = append(names, n.appNames()...)
		}
	}
	return strings.Join(names, ", ")
}

func runTabSend(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "id", "text", "key")
	if err != nil {
		return err
	}
	if err := set.Reject("id", "text", "key"); err != nil {
		return err
	}
	id, text, key := set.String("id", ""), set.String("text", ""), set.String("key", "")
	if id == "" {
		return fmt.Errorf("--id is required: a tab id from `aos tab list`")
	}
	if (text == "") == (key == "") {
		return fmt.Errorf("give exactly one of --text or --key")
	}
	t, err := pickTab(tabFilter{ID: id})
	if err != nil {
		return err
	}
	if t.ID != id {
		t = withPane(t, id)
	}
	p, err := providerFor(t)
	if err != nil {
		return err
	}
	ctx, cancel := tabCtx()
	defer cancel()
	return p.send(ctx, t, text, key)
}

func runTabClose(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "kind", "title", "url", "id")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "kind", "title", "url", "id", "exact"); err != nil {
		return err
	}
	f, err := tabFilterFrom(set)
	if err != nil {
		return err
	}
	t, err := pickTab(f)
	if err != nil {
		return err
	}
	p, err := providerFor(t)
	if err != nil {
		return err
	}
	ctx, cancel := tabCtx()
	defer cancel()
	if err := p.close(ctx, t); err != nil {
		return err
	}
	c.Printf("closed %s  %s\n", t.ID, t.Title)
	return nil
}

// tabKeyChord splits "ctrl+shift+c" into its key and modifiers, normalized
// to the names terminals' scripting dictionaries use.
func tabKeyChord(combo string) (key string, mods []string, err error) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(combo)), "+")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return "", nil, fmt.Errorf("bad key %q: expected e.g. enter, ctrl+c, cmd+shift+t", combo)
	}
	key = parts[len(parts)-1]
	for _, m := range parts[:len(parts)-1] {
		switch m {
		case "ctrl", "control":
			mods = append(mods, "control")
		case "shift":
			mods = append(mods, "shift")
		case "alt", "opt", "option":
			mods = append(mods, "option")
		case "cmd", "command", "super", "win", "meta":
			mods = append(mods, "command")
		default:
			return "", nil, fmt.Errorf("bad modifier %q in %q: use ctrl, shift, alt, cmd", m, combo)
		}
	}
	switch key {
	case "return":
		key = "enter"
	case "esc":
		key = "escape"
	}
	return key, mods, nil
}
