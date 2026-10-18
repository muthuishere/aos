package groups

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/muthuishere/aos/internal/engine"
)

// Scripted tab providers: one JXA (osascript -l JavaScript) program per app
// family and action. Listing reads properties in bulk — `windows.tabs.title()`
// is one Apple Event for every tab of every window, where a per-tab loop is
// one event each (97 ms against 1.8 s for 63 Chrome tabs). Every script
// first checks `running()`, which never launches the app.

// runJXA runs a JXA program with argv and returns its stdout. Tests replace it.
var runJXA = func(ctx context.Context, script string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", append([]string{"-l", "JavaScript", "-e", script}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("timed out: %w", ctx.Err())
		}
		return "", errors.New(strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// scriptErr turns an osascript failure into something a person can act on.
// -1743 is macOS refusing the Apple Event: the Automation permission for
// this app was denied (or its first-use prompt dismissed).
func scriptErr(app string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "-1743") || strings.Contains(msg, "Not authorized to send Apple events"):
		return fmt.Errorf("%s: macOS blocked aos from controlling it (Automation permission). "+
			"Fix: System Settings > Privacy & Security > Automation, find the app running aos (your terminal) "+
			"and turn on %s; or run `tccutil reset AppleEvents` and retry to get the prompt again", app, app)
	case strings.Contains(msg, "aos-gone:"):
		_, after, _ := strings.Cut(msg, "aos-gone:")
		what, _, _ := strings.Cut(after, "(")
		return fmt.Errorf("%s: %s is gone (list again with `aos tab list`)", app, strings.TrimSpace(what))
	case strings.Contains(msg, "-600"):
		return fmt.Errorf("%s is not running", app)
	}
	// "execution error: Error: Error: <what the script threw> (-2700)"
	if _, after, ok := strings.Cut(msg, "execution error: "); ok {
		msg = after
	}
	for strings.HasPrefix(msg, "Error: ") {
		msg = strings.TrimPrefix(msg, "Error: ")
	}
	msg = strings.TrimSuffix(msg, " (-2700)")
	return fmt.Errorf("%s: %s", app, msg)
}

// jsHelpers is prepended to every script that addresses one window or tab.
// Window and tab ids are compared as strings: Chrome's are numbers, Ghostty's
// are strings, and `windows.byId` is not in every dictionary.
const jsHelpers = `
function findWin(A, id) { var ws = A.windows(); for (var i = 0; i < ws.length; i++) { if (String(ws[i].id()) == id) return ws[i]; } throw new Error("aos-gone: window " + id); }
function findTab(w, key, byId) { var ts = w.tabs(); for (var i = 0; i < ts.length; i++) { if (byId ? String(ts[i].id()) == key : String(i + 1) == key) return [ts[i], i + 1]; } throw new Error("aos-gone: tab " + key); }
function app(name) { var A = Application(name); A.includeStandardAdditions = false; return A; }
`

// scriptWindow is the one shape every family's list script returns.
type scriptWindow struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Bounds *struct {
		X, Y, Width, Height float64
	} `json:"bounds"`
	Minimized bool        `json:"minimized"`
	Tabs      []scriptTab `json:"tabs"`
}

type scriptTab struct {
	ID     string `json:"id"`
	Index  int    `json:"index"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	CWD    string `json:"cwd"`
	Active bool   `json:"active"`
	Panes  []Pane `json:"panes"`
}

type scriptList struct {
	Running bool           `json:"running"`
	Windows []scriptWindow `json:"windows"`
}

// parseScriptTabs turns a list script's JSON into tabs with stable ids:
// <slug>:<window id>:<tab id, or index when the app has none>[:<pane id>].
func parseScriptTabs(slug, app, out string) ([]Tab, error) {
	var l scriptList
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		return nil, fmt.Errorf("%s: unreadable tab list: %v", app, err)
	}
	var tabs []Tab
	for _, w := range l.Windows {
		var bounds *engine.Rect
		if w.Bounds != nil {
			b := w.Bounds
			// Chrome reports {x,y,width,height}; Safari, iTerm and
			// Terminal report the same through JXA's rectangle record.
			bounds = &engine.Rect{X: int(b.X), Y: int(b.Y), W: int(b.Width), H: int(b.Height)}
		}
		for _, st := range w.Tabs {
			key := st.ID
			if key == "" {
				key = strconv.Itoa(st.Index)
			}
			t := Tab{
				ID:           slug + ":" + w.ID + ":" + key,
				App:          app,
				NativeWindow: w.ID,
				Index:        st.Index,
				Title:        st.Title,
				URL:          st.URL,
				CWD:          st.CWD,
				Active:       st.Active,
				Source:       "applescript",
				windowTitle:  w.Title,
				windowBounds: bounds,
				minimized:    w.Minimized,
			}
			if len(st.Panes) > 1 {
				for _, p := range st.Panes {
					p.ID = t.ID + ":" + p.ID
					t.Panes = append(t.Panes, p)
				}
			}
			tabs = append(tabs, t)
		}
	}
	return tabs, nil
}

// tabRef is a tab id split back into what the scripts need.
type tabRef struct {
	slug, window, tab, pane string
}

func refOf(t Tab) (tabRef, error) {
	slug, parts, err := splitTabID(t.ID)
	if err != nil {
		return tabRef{}, err
	}
	r := tabRef{slug: slug, window: parts[0], tab: parts[1]}
	for _, p := range t.Panes {
		if p.Active && len(t.Panes) > 1 {
			_, pp, _ := splitTabID(p.ID)
			if len(pp) == 3 {
				r.pane = pp[2]
			}
		}
	}
	if len(parts) == 3 {
		r.pane = parts[2]
	}
	return r, nil
}

// scriptFamily is one app family's scripts.
type scriptFamily struct {
	apps    map[string]string // app name -> id slug
	listJS  string
	focusJS string // argv: app, window, tab, pane
	// selectJS selects a tab in its window without raising the window or
	// activating the app (argv: app, window, tab), for the page commands.
	selectJS string
	openJS   string // argv: app, url, window, new("1"), background("1"), cwd, command
	sendJS   string // argv: app, window, tab, pane, text, key, modifiers
	closeJS  string // argv: app, window, tab
	noSend   string // why send is refused, when send is ""
	noOpen   string // extra open restriction, checked by the family itself
	// plainCommand passes --cwd/--command as a plain shell line (the app
	// types it into a fresh shell) instead of the tab's start program.
	plainCommand bool
	// listedAs maps the name scripting answers to onto the name the window
	// list shows, where they differ ("iTerm" is listed as "iTerm2").
	listedAs map[string]string
}

// shown is the app name as the window list prints it.
func (f *scriptFamily) shown(app string) string {
	if n, ok := f.listedAs[app]; ok {
		return n
	}
	return app
}

// scripted is the inverse of shown.
func (f *scriptFamily) scripted(app string) string {
	for s, n := range f.listedAs {
		if n == app {
			return s
		}
	}
	return app
}

func (f *scriptFamily) owns(slug string) bool {
	for _, s := range f.apps {
		if s == slug {
			return true
		}
	}
	return false
}

func (f *scriptFamily) handles(app string) bool {
	_, ok := f.appFor(app)
	return ok
}

func (f *scriptFamily) appNames() []string {
	var out []string
	for a := range f.apps {
		out = append(out, a)
	}
	return out
}

// appFor resolves a user's app query ("chrome", "Google Chrome") to the
// family's real app name.
func (f *scriptFamily) appFor(query string) (string, bool) {
	for a := range f.apps {
		if strings.EqualFold(a, query) {
			return a, true
		}
	}
	for a, slug := range f.apps {
		if strings.EqualFold(slug, query) {
			return a, true
		}
	}
	for a := range f.apps {
		if appMatches(a, query) || appMatches(f.shown(a), query) {
			return a, true
		}
	}
	return "", false
}

func (f *scriptFamily) list(ctx context.Context, filter tabFilter, _ []string) ([]Tab, error) {
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		tabs []Tab
		errs []string
	)
	for app, slug := range f.apps {
		if !appMatches(f.shown(app), filter.App) && !strings.EqualFold(slug, filter.App) ||
			filter.Kind != "" && windowKind(app) != filter.Kind {
			continue
		}
		wg.Add(1)
		go func(app, slug string) {
			defer wg.Done()
			out, err := runJXA(ctx, jsHelpers+f.listJS, app)
			var got []Tab
			if err == nil {
				got, err = parseScriptTabs(slug, f.shown(app), out)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, scriptErr(app, err).Error())
				return
			}
			tabs = append(tabs, got...)
		}(app, slug)
	}
	wg.Wait()
	if len(errs) > 0 {
		return tabs, errors.New(strings.Join(errs, "; "))
	}
	return tabs, nil
}

func (f *scriptFamily) run(ctx context.Context, script, app string, args ...string) (string, error) {
	out, err := runJXA(ctx, jsHelpers+script, append([]string{app}, args...)...)
	if err != nil {
		return "", scriptErr(app, err)
	}
	return out, nil
}

func (f *scriptFamily) focus(ctx context.Context, t Tab) error {
	r, err := refOf(t)
	if err != nil {
		return err
	}
	_, err = f.run(ctx, f.focusJS, f.scripted(t.App), r.window, r.tab, r.pane)
	if err == nil {
		// activate() from a background process is sometimes ignored;
		// LaunchServices always brings the app forward.
		_ = exec.Command("/usr/bin/open", "-a", t.App).Run()
	}
	return err
}

// selectQuiet selects t in its window, leaving the window where it is.
func (f *scriptFamily) selectQuiet(ctx context.Context, t Tab) error {
	if f.selectJS == "" {
		return errTabUnsupported{fmt.Sprintf("aos cannot select %s tabs without raising the window", t.App)}
	}
	r, err := refOf(t)
	if err != nil {
		return err
	}
	_, err = f.run(ctx, f.selectJS, f.scripted(t.App), r.window, r.tab)
	return err
}

func (f *scriptFamily) close(ctx context.Context, t Tab) error {
	if f.closeJS == "" {
		return errTabUnsupported{fmt.Sprintf("%s cannot close a single tab from scripting; focus it with `aos tab focus --id=%s` and press its close shortcut", t.App, t.ID)}
	}
	r, err := refOf(t)
	if err != nil {
		return err
	}
	_, err = f.run(ctx, f.closeJS, f.scripted(t.App), r.window, r.tab)
	return err
}

func (f *scriptFamily) send(ctx context.Context, t Tab, text, key string) error {
	if f.sendJS == "" {
		return errTabUnsupported{fmt.Sprintf("%s tabs cannot receive input from aos: %s", t.App, f.noSend)}
	}
	r, err := refOf(t)
	if err != nil {
		return err
	}
	var mods []string
	if key != "" {
		if key, mods, err = tabKeyChord(key); err != nil {
			return err
		}
	}
	_, err = f.run(ctx, f.sendJS, f.scripted(t.App), r.window, r.tab, r.pane, text, key, strings.Join(mods, ","))
	return err
}

func (f *scriptFamily) open(ctx context.Context, req tabOpenRequest) (string, error) {
	app, _ := f.appFor(req.App)
	if f.noOpen != "" && !req.NewWindow {
		if req.Window != "" {
			return "", errTabUnsupported{f.noOpen}
		}
		req.NewWindow = true
	}
	command := ""
	switch {
	case f.plainCommand:
		command = plainShellLine(req.CWD, req.Command)
	case req.Command != "" || req.CWD != "":
		command = shellCommand(req.CWD, req.Command)
	}
	out, err := f.run(ctx, f.openJS, app, req.URL, req.Window, flag01(req.NewWindow), flag01(req.Background), req.CWD, command)
	if err != nil {
		return "", err
	}
	var got struct {
		Window, Tab string
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		return "", fmt.Errorf("%s: unreadable reply %q", app, out)
	}
	return f.apps[app] + ":" + got.Window + ":" + got.Tab, nil
}

func flag01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// shellCommand builds the program a terminal tab starts with: the user's
// shell runs the command, then stays as an interactive shell, so the tab
// does not vanish the moment the command exits. Passing it as the tab's
// command (rather than typing it in) avoids the double echo of input that
// arrives before the prompt.
func shellCommand(cwd, command string) string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	var script string
	if cwd != "" {
		script = "cd " + shellQuote(cwd) + " && "
	}
	if command != "" {
		script += command + "; "
	}
	script += "exec " + shell + " -l"
	return shell + " -lc " + shellQuote(script)
}

// plainShellLine is what a terminal that types its command into a new
// shell runs: cd, then the command.
func plainShellLine(cwd, command string) string {
	var parts []string
	if cwd != "" {
		parts = append(parts, "cd "+shellQuote(cwd))
	}
	if command != "" {
		parts = append(parts, command)
	}
	return strings.Join(parts, " && ")
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ---- the families -------------------------------------------------------

// chromeFamily drives Chrome and every browser built on its dictionary.
// Arc is left out: its dictionary has spaces instead of windows of tabs.
func chromeFamily() *scriptFamily {
	return &scriptFamily{
		apps: map[string]string{
			"Google Chrome": "chrome", "Google Chrome Canary": "canary", "Chromium": "chromium",
			"Microsoft Edge": "edge", "Brave Browser": "brave", "Vivaldi": "vivaldi",
		},
		listJS: `function run(argv) {
  var A; try { A = app(argv[0]); if (!A.running()) return JSON.stringify({running: false}); } catch (e) { return JSON.stringify({running: false}); }
  var ids = A.windows.id(), out = [];
  if (ids.length) {
    var n = A.windows.name(), at = A.windows.activeTabIndex(), b = A.windows.bounds(), m = A.windows.minimized();
    var ti = A.windows.tabs.id(), tt = A.windows.tabs.title(), tu = A.windows.tabs.url();
    for (var i = 0; i < ids.length; i++) {
      var tabs = [];
      for (var j = 0; j < ti[i].length; j++) tabs.push({id: String(ti[i][j]), index: j + 1, title: tt[i][j] || "", url: tu[i][j] || "", active: at[i] == j + 1});
      out.push({id: String(ids[i]), title: n[i] || "", bounds: b[i], minimized: m[i], tabs: tabs});
    }
  }
  return JSON.stringify({running: true, windows: out});
}`,
		focusJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], true);
  w.activeTabIndex = r[1];
  if (w.minimized()) w.minimized = false;
  w.index = 1; A.activate(); return "ok";
}`,
		selectJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], true);
  w.activeTabIndex = r[1]; return "ok";
}`,
		openJS: `function run(argv) {
  var A = app(argv[0]), url = argv[1], w, t, bg = argv[4] == "1";
  if (argv[3] == "1" || A.windows.length == 0) {
    w = A.Window().make(); t = w.tabs[0]; if (url) t.url = url;
  } else {
    w = argv[2] ? findWin(A, argv[2]) : A.windows[0];
    var prev = w.activeTabIndex(), props = {};
    if (url) props.url = url;
    w.tabs.push(A.Tab(props));
    var n = w.tabs.length; t = w.tabs[n - 1];
    w.activeTabIndex = bg ? prev : n;
  }
  if (!bg) { w.index = 1; A.activate(); }
  return JSON.stringify({window: String(w.id()), tab: String(t.id())});
}`,
		closeJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], true);
  r[0].close(); return "ok";
}`,
		noSend: "a browser tab has no input channel; focus it and use `aos key` / `aos type`",
	}
}

func safariFamily() *scriptFamily {
	return &scriptFamily{
		apps: map[string]string{"Safari": "safari"},
		listJS: `function run(argv) {
  var A; try { A = app(argv[0]); if (!A.running()) return JSON.stringify({running: false}); } catch (e) { return JSON.stringify({running: false}); }
  var ws = A.windows(), out = [];
  for (var i = 0; i < ws.length; i++) {
    var w = ws[i];
    try {
      var tn = w.tabs.name(), tu = w.tabs.url(), ci = 0;
      try { ci = w.currentTab.index(); } catch (e) {}
      var tabs = [];
      for (var j = 0; j < tn.length; j++) tabs.push({index: j + 1, title: tn[j] || "", url: tu[j] || "", active: ci == j + 1});
      out.push({id: String(w.id()), title: w.name() || "", bounds: w.bounds(), minimized: w.miniaturized(), tabs: tabs});
    } catch (e) {}
  }
  return JSON.stringify({running: true, windows: out});
}`,
		focusJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  w.currentTab = r[0];
  if (w.miniaturized()) w.miniaturized = false;
  w.index = 1; A.activate(); return "ok";
}`,
		selectJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  w.currentTab = r[0]; return "ok";
}`,
		openJS: `function run(argv) {
  var A = app(argv[0]), url = argv[1], w, bg = argv[4] == "1";
  if (argv[3] == "1" || A.windows.length == 0) {
    // windows[0] after make() is not always the new window: find it by id.
    var before = A.windows.id(); A.Document().make(); w = A.windows[0];
    var ws = A.windows();
    for (var i = 0; i < ws.length; i++) { if (before.indexOf(ws[i].id()) < 0) { w = ws[i]; break; } }
    if (url) w.currentTab.url = url;
  } else {
    w = argv[2] ? findWin(A, argv[2]) : A.windows[0];
    var prev = w.currentTab(), props = {};
    if (url) props.url = url;
    w.tabs.push(A.Tab(props));
    w.currentTab = bg ? prev : w.tabs[w.tabs.length - 1];
  }
  if (!bg) { w.index = 1; A.activate(); }
  return JSON.stringify({window: String(w.id()), tab: String(w.tabs.length)});
}`,
		closeJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  r[0].close(); return "ok";
}`,
		noSend: "a browser tab has no input channel; focus it and use `aos key` / `aos type`",
	}
}

func ghosttyFamily() *scriptFamily {
	return &scriptFamily{
		apps: map[string]string{"Ghostty": "ghostty"},
		listJS: `function run(argv) {
  var A; try { A = app(argv[0]); if (!A.running()) return JSON.stringify({running: false}); } catch (e) { return JSON.stringify({running: false}); }
  var ids = A.windows.id(), out = [];
  if (ids.length) {
    var n = A.windows.name(), ti = A.windows.tabs.id(), tn = A.windows.tabs.name(), ts = A.windows.tabs.selected();
    var pi = A.windows.tabs.terminals.id(), pn = A.windows.tabs.terminals.name(), pc = A.windows.tabs.terminals.workingDirectory();
    var ft = A.windows.tabs.focusedTerminal.id();
    for (var i = 0; i < ids.length; i++) {
      var tabs = [];
      for (var j = 0; j < ti[i].length; j++) {
        var panes = [], cwd = "";
        for (var k = 0; k < pi[i][j].length; k++) {
          var active = pi[i][j][k] == ft[i][j];
          if (active) cwd = pc[i][j][k] || "";
          panes.push({id: String(pi[i][j][k]), title: pn[i][j][k] || "", cwd: pc[i][j][k] || "", active: active});
        }
        tabs.push({id: String(ti[i][j]), index: j + 1, title: tn[i][j] || "", cwd: cwd, active: ts[i][j], panes: panes});
      }
      out.push({id: String(ids[i]), title: n[i] || "", tabs: tabs});
    }
  }
  return JSON.stringify({running: true, windows: out});
}`,
		focusJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], true);
  A.selectTab(r[0]);
  if (argv[3]) { var ps = r[0].terminals(); for (var i = 0; i < ps.length; i++) if (String(ps[i].id()) == argv[3]) A.focus(ps[i]); }
  A.activateWindow(w); A.activate(); return "ok";
}`,
		openJS: `function run(argv) {
  var A = app(argv[0]), w, t, bg = argv[4] == "1";
  var c = A.newSurfaceConfiguration();
  if (argv[5]) c.initialWorkingDirectory = argv[5];
  if (argv[6]) c.command = argv[6];
  if (argv[3] == "1" || A.windows.length == 0) {
    w = A.newWindow({withConfiguration: c}); t = w.selectedTab();
  } else {
    w = argv[2] ? findWin(A, argv[2]) : A.frontWindow();
    var prev = w.selectedTab();
    t = A.newTab({in: w, withConfiguration: c});
    if (bg) { delay(0.3); A.selectTab(prev); }
  }
  if (!bg) A.activate();
  return JSON.stringify({window: String(w.id()), tab: String(t.id())});
}`,
		sendJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], true), term = r[0].focusedTerminal();
  if (argv[3]) { var ps = r[0].terminals(); for (var i = 0; i < ps.length; i++) if (String(ps[i].id()) == argv[3]) term = ps[i]; }
  if (argv[4]) { A.inputText(argv[4], {to: term}); return "ok"; }
  var o = {to: term}; if (argv[6]) o.modifiers = argv[6];
  A.sendKey(argv[5], o); return "ok";
}`,
		closeJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], true);
  A.closeTab(r[0]); return "ok";
}`,
	}
}

// itermFamily: iTerm2 tabs have no id, so a tab is its index; every
// session (split pane) has a stable unique id.
func itermFamily() *scriptFamily {
	return &scriptFamily{
		apps:     map[string]string{"iTerm": "iterm"},
		listedAs: map[string]string{"iTerm": "iTerm2"},
		listJS: `function run(argv) {
  var A; try { A = app(argv[0]); if (!A.running()) return JSON.stringify({running: false}); } catch (e) { return JSON.stringify({running: false}); }
  var ws = A.windows(), out = [];
  for (var i = 0; i < ws.length; i++) {
    var w = ws[i], tabs = [], ct = -1; // iTerm tab indexes are 0-based
    try { ct = w.currentTab().index(); } catch (e) {}
    var ts = w.tabs();
    for (var j = 0; j < ts.length; j++) {
      var cs = ts[j].currentSession(), ss = ts[j].sessions(), panes = [], cwd = "", title = "";
      for (var k = 0; k < ss.length; k++) {
        var p = "", active = ss[k].id() == cs.id();
        try { p = ss[k].variable({named: "path"}) || ""; } catch (e) {}
        if (active) { cwd = p; title = ss[k].name(); }
        panes.push({id: String(ss[k].id()), title: ss[k].name() || "", cwd: p, active: active});
      }
      tabs.push({index: j + 1, title: title, cwd: cwd, active: ts[j].index() == ct, panes: panes});
    }
    out.push({id: String(w.id()), title: w.name() || "", bounds: w.bounds(), minimized: w.miniaturized(), tabs: tabs});
  }
  return JSON.stringify({running: true, windows: out});
}`,
		focusJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  r[0].select();
  if (argv[3]) { var ss = r[0].sessions(); for (var i = 0; i < ss.length; i++) if (String(ss[i].id()) == argv[3]) ss[i].select(); }
  if (w.miniaturized()) w.miniaturized = false;
  w.select(); A.activate(); return "ok";
}`,
		openJS: `function run(argv) {
  var A = app(argv[0]), w, bg = argv[4] == "1", o = {};
  if (argv[6]) o.command = argv[6];
  if (argv[3] == "1" || A.windows.length == 0) {
    w = A.createWindowWithDefaultProfile(o);
  } else {
    w = argv[2] ? findWin(A, argv[2]) : A.currentWindow();
    var prev = w.currentTab();
    w.createTabWithDefaultProfile(o);
    if (bg) prev.select();
  }
  if (!bg) A.activate();
  return JSON.stringify({window: String(w.id()), tab: String(w.tabs().length)});
}`,
		sendJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false), s = r[0].currentSession();
  if (argv[3]) { var ss = r[0].sessions(); for (var i = 0; i < ss.length; i++) if (String(ss[i].id()) == argv[3]) s = ss[i]; }
  if (argv[4]) { s.write({text: argv[4], newline: false}); return "ok"; }
  var k = argv[5], mods = argv[6];
  if (k == "enter" && !mods) { s.write({text: "", newline: true}); return "ok"; }
  if (mods == "control" && k.length == 1 && k >= "a" && k <= "z") { s.write({text: String.fromCharCode(k.charCodeAt(0) - 96), newline: false}); return "ok"; }
  throw new Error("iTerm2 takes only enter and ctrl+<letter> as keys");
}`,
		closeJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  r[0].close(); return "ok";
}`,
	}
}

// terminalFamily: Terminal.app tabs have no id, no cwd, and no scripted
// way to make a new tab or close one; it can list, select, open a new
// window, and run a command in a tab.
func terminalFamily() *scriptFamily {
	return &scriptFamily{
		apps: map[string]string{"Terminal": "terminal"},
		listJS: `function run(argv) {
  var A; try { A = app(argv[0]); if (!A.running()) return JSON.stringify({running: false}); } catch (e) { return JSON.stringify({running: false}); }
  var ws = A.windows(), out = [];
  for (var i = 0; i < ws.length; i++) {
    var w = ws[i];
    try {
      var ts = w.tabs(), tabs = [];
      for (var j = 0; j < ts.length; j++) {
        var t = ts[j], title = "";
        try { title = t.customTitle(); } catch (e) {}
        if (!title) { try { var ps = t.processes(); title = ps[ps.length - 1]; } catch (e) {} }
        tabs.push({index: j + 1, title: title || "", active: t.selected()});
      }
      out.push({id: String(w.id()), title: w.name() || "", bounds: w.bounds(), minimized: w.miniaturized(), tabs: tabs});
    } catch (e) {}
  }
  return JSON.stringify({running: true, windows: out});
}`,
		focusJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  w.selectedTab = r[0];
  if (w.miniaturized()) w.miniaturized = false;
  w.index = 1; A.activate(); return "ok";
}`,
		openJS: `function run(argv) {
  var A = app(argv[0]);
  var t = argv[6] ? A.doScript(argv[6]) : A.doScript("");
  var w = A.windows[0];
  if (argv[4] != "1") A.activate();
  return JSON.stringify({window: String(w.id()), tab: "1"});
}`,
		sendJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  if (!argv[4]) throw new Error("Terminal.app takes only --text, which it runs as a command");
  A.doScript(argv[4], {in: r[0]}); return "ok";
}`,
		// A window's only tab closes with its window; Terminal has no
		// per-tab close, and a many-tab window is never closed whole.
		closeJS: `function run(argv) {
  var A = app(argv[0]), w = findWin(A, argv[1]), r = findTab(w, argv[2], false);
  if (w.tabs.length != 1) throw new Error("Terminal.app has no scripted close for one tab of several; focus it and press cmd+w");
  // A busy tab makes Terminal ask "terminate running processes?", a sheet
  // nobody would answer. Refuse instead.
  if (r[0].busy()) throw new Error("the tab is running a process (" + r[0].processes().join(", ") + "); stop it first, aos will not answer Terminal's terminate prompt");
  w.close(); return "ok";
}`,
		noOpen:       "Terminal.app cannot add a tab to an existing window from scripting; drop --window to open a new window",
		plainCommand: true,
	}
}
