package groups

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/muthuishere/aos/internal/engine"
)

// axTabs is the catch-all provider: it reads tab strips out of the
// accessibility tree (AX on macOS, UI Automation on Windows). It sees
// titles only — no URLs, no cwd — and is slower, so on macOS it only covers
// apps no scripted provider handles. Ids are <source>:<aos window id>:<index>.
type axTabs struct {
	source     string                  // "ax" or "uia"
	skip       func(app string) bool   // apps another provider owns
	closeChord func(app string) string // the app's close-tab shortcut, "" when unknown
	opener     func(context.Context, tabOpenRequest) (string, error)
	texts      func(string) ([]engine.TextItem, error)
	windows    func() ([]engine.Window, error)
	// Seams for tests: the real ones press keys and raise windows.
	pressKey  func(combo string) error
	sleep     func(time.Duration)
	selectTab func(context.Context, Tab) error
}

func newAXTabs(source string, closeChord func(string) string, skip func(string) bool) *axTabs {
	a := &axTabs{
		source: source, closeChord: closeChord, skip: skip,
		pressKey: engine.PressKey,
		sleep:    time.Sleep,
		texts:    engine.WindowTexts,
		windows:  func() ([]engine.Window, error) { return engine.ListWindows(engine.Filter{}) },
	}
	a.selectTab = a.focus
	return a
}

func (a *axTabs) owns(slug string) bool { return slug == a.source }

func (a *axTabs) handles(app string) bool { return a.skip == nil || !a.skip(app) }

// edgeMemory is the suffix Edge appends to tab names in UI Automation.
var edgeMemory = regexp.MustCompile(`\s+-\s+Memory usage\s+-\s+[\d.,]+\s*[KMG]B$`)

func cleanTabTitle(s string) string { return strings.TrimSpace(edgeMemory.ReplaceAllString(s, "")) }

func (a *axTabs) list(ctx context.Context, _ tabFilter, apps []string) ([]Tab, error) {
	want := map[string]bool{}
	for _, app := range apps {
		if a.handles(app) {
			want[app] = true
		}
	}
	if len(want) == 0 {
		return nil, nil
	}
	ws, err := a.windows()
	if err != nil {
		return nil, err
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		tabs []Tab
	)
	for _, w := range ws {
		if !want[w.App] {
			continue
		}
		wg.Add(1)
		go func(w engine.Window) {
			defer wg.Done()
			done := make(chan []engine.TextItem, 1)
			go func() {
				items, err := a.texts(w.ID)
				if err != nil {
					items = nil
				}
				done <- items
			}()
			select {
			case items := <-done:
				got := axTabsOf(a.source, w, items)
				mu.Lock()
				tabs = append(tabs, got...)
				mu.Unlock()
			case <-ctx.Done():
				// An app with a huge tree is left out rather than stalling the list.
			}
		}(w)
	}
	wg.Wait()
	return tabs, nil
}

// axTabsOf picks the tab strip out of a window's accessibility elements:
// AXTab / UIA TabItem elements inside the window's bounds (the walk also
// covers the app's menu bar). AXRadioButton is not counted: some macOS tab
// bars use it, but so do view switchers and label pickers, and a wrong tab
// is worse than a missing one.
func axTabsOf(source string, w engine.Window, items []engine.TextItem) []Tab {
	kind := windowKind(w.App)
	var tabs []Tab
	for _, it := range items {
		if it.Role != "tab" || !insideRect(it.Bounds, w.Bounds) {
			continue
		}
		title := cleanTabTitle(it.Text)
		if title == "" {
			continue
		}
		idx := len(tabs) + 1
		center := engine.Point{X: it.Bounds.X + it.Bounds.W/2, Y: it.Bounds.Y + it.Bounds.H/2}
		tabs = append(tabs, Tab{
			ID:           source + ":" + w.ID + ":" + strconv.Itoa(idx),
			App:          w.App,
			Kind:         kind,
			Window:       w.ID,
			NativeWindow: w.ID,
			Index:        idx,
			Title:        title,
			Source:       source,
			center:       &center,
		})
	}
	// The selected tab is the one the window is titled after
	// ("Inbox - Gmail - Microsoft Edge", "Inbox - Gmail - Google Chrome").
	best := -1
	for i, t := range tabs {
		if strings.HasPrefix(w.Title, t.Title) && (best < 0 || len(t.Title) > len(tabs[best].Title)) {
			best = i
		}
	}
	if best >= 0 {
		tabs[best].Active = true
	}
	return tabs
}

func insideRect(r, outer engine.Rect) bool {
	cx, cy := r.X+r.W/2, r.Y+r.H/2
	return cx >= outer.X && cx < outer.X+outer.W && cy >= outer.Y && cy < outer.Y+outer.H
}

func (a *axTabs) windowOf(t Tab) (engine.Window, error) {
	ws, err := a.windows()
	if err != nil {
		return engine.Window{}, err
	}
	for _, w := range ws {
		if w.ID == t.Window {
			return w, nil
		}
	}
	return engine.Window{}, fmt.Errorf("%s: window %s is gone (list again with `aos tab list`)", t.App, t.Window)
}

func (a *axTabs) focus(_ context.Context, t Tab) error {
	if t.center == nil {
		return fmt.Errorf("%s: no position for tab %s", t.App, t.ID)
	}
	w, err := a.windowOf(t)
	if err != nil {
		return err
	}
	if err := engine.FocusWindowID(w.ID); err != nil {
		return err
	}
	if err := engine.PressElement(w, t.center.X, t.center.Y); err == nil {
		return nil
	}
	// No accessibility action on the tab: click it, the window is in front.
	time.Sleep(150 * time.Millisecond)
	return engine.ClickAt(t.center.X, t.center.Y)
}

// selectQuiet selects the tab with an accessibility press (UIA
// SelectionItem/Invoke), which neither raises the window nor moves the mouse.
func (a *axTabs) selectQuiet(_ context.Context, t Tab) error {
	if t.center == nil {
		return fmt.Errorf("%s: no position for tab %s", t.App, t.ID)
	}
	w, err := a.windowOf(t)
	if err != nil {
		return err
	}
	return engine.PressElement(w, t.center.X, t.center.Y)
}

// close selects the tab, then sends the app's close-tab shortcut. It is
// best-effort, because a key press goes to whatever is frontmost:
//   - apps whose shortcut aos does not know are refused, not guessed (in a
//     terminal ctrl+w reaches the shell);
//   - the frontmost window is re-checked immediately before the key, and the
//     key is not sent unless it is the tab's window;
//   - success is reported only when the window's tab count dropped.
func (a *axTabs) close(ctx context.Context, t Tab) error {
	chord := a.closeChord(t.App)
	if chord == "" {
		return errTabUnsupported{fmt.Sprintf("aos does not know %s's close-tab shortcut; focus the tab (`aos tab focus --id=%s`) and close it yourself", t.App, t.ID)}
	}
	before, err := a.texts(t.Window)
	if err != nil {
		return err
	}
	w, err := a.windowOf(t)
	if err != nil {
		return err
	}
	count := len(axTabsOf(a.source, w, before))
	if err := a.selectTab(ctx, t); err != nil {
		return err
	}
	a.sleep(80 * time.Millisecond) // let the selection land before checking focus
	front, err := a.frontmost()
	if err != nil {
		return err
	}
	if front != t.Window {
		return fmt.Errorf("%s: its window is not in front (front is %q); not sending %s", t.App, front, chord)
	}
	if err := a.pressKey(chord); err != nil {
		return err
	}
	for i := 0; i < 10; i++ {
		a.sleep(100 * time.Millisecond)
		items, err := a.texts(t.Window)
		if err != nil {
			if _, gone := a.windowOf(t); gone != nil {
				return nil // the window closed with its last tab
			}
			continue
		}
		if len(axTabsOf(a.source, w, items)) < count {
			return nil
		}
	}
	return fmt.Errorf("%s: sent %s but the tab count did not drop; the tab may still be open", t.App, chord)
}

// frontmost is the focused window's aos id.
func (a *axTabs) frontmost() (string, error) {
	ws, err := a.windows()
	if err != nil {
		return "", err
	}
	for _, w := range ws {
		if w.Focused {
			return w.ID, nil
		}
	}
	return "", nil
}

func (a *axTabs) send(_ context.Context, t Tab, _, _ string) error {
	return errTabUnsupported{fmt.Sprintf("%s tabs are read through accessibility, which has no input channel; focus the tab (`aos tab focus --id=%s`) and use `aos type` / `aos key`", t.App, t.ID)}
}

func (a *axTabs) open(ctx context.Context, req tabOpenRequest) (string, error) {
	if a.opener == nil {
		return "", errTabUnsupported{fmt.Sprintf("aos cannot open tabs in %s; it knows %s", req.App, knownTabApps())}
	}
	return a.opener(ctx, req)
}
