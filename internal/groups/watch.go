package groups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("watch", "Follow what changes on this machine, one JSON line per event")
		r.Add(
			&cli.Command{
				Group: "watch", Name: "clipboard",
				Summary:  "Print a JSON line whenever the clipboard text changes",
				Blocking: true,
				Args:     "[--interval=<500ms>] [--max=<n>] [--content]",
				Examples: []string{
					"aos watch clipboard",
					"aos watch clipboard --max=1 --content",
				},
				Run: runWatchClipboard,
			},
			&cli.Command{
				Group: "watch", Name: "window",
				Summary:  "Print a JSON line whenever the focused window changes",
				Blocking: true,
				Args:     "[--interval=<500ms>] [--max=<n>]",
				Examples: []string{
					"aos watch window",
					"aos watch window --max=1",
				},
				Run: runWatchWindow,
			},
			&cli.Command{
				Group: "watch", Name: "file",
				Summary:  "Print a JSON line whenever a file or directory entry changes",
				Blocking: true,
				Args:     "<path> [--interval=<500ms>] [--max=<n>] [--recursive]",
				Examples: []string{
					"aos watch file ~/Downloads",
					"aos watch file ./notes.md --max=1",
					"aos watch file ./src --recursive --interval=1s",
				},
				Run: runWatchFile,
			},
		)
	})
}

// defaultWatchInterval is a compromise: fast enough that a change feels caught
// as it happens, slow enough that polling `pbpaste` or the window list forever
// costs nothing anyone will notice.
const defaultWatchInterval = 500 * time.Millisecond

// minWatchInterval keeps a typo like `--interval=1` from turning a watcher into
// a busy loop that shells out thousands of times a second.
const minWatchInterval = 50 * time.Millisecond

// watchEvent is the one shape every watcher emits. A consumer should be able to
// read any `watch` stream with the same parser, so the envelope — event, at,
// seq — is fixed and each watcher fills only the fields it has. Pointers mark
// the numeric fields that have a meaningful zero (an empty clipboard, monitor 0
// meaning off-screen) so they are omitted rather than reported as 0.
type watchEvent struct {
	Event string `json:"event"`
	At    string `json:"at"`
	Seq   int    `json:"seq"`

	// clipboard
	Length *int   `json:"length,omitempty"`
	Digest string `json:"digest,omitempty"`
	Text   string `json:"text,omitempty"`

	// window
	App      string `json:"app,omitempty"`
	Title    string `json:"title,omitempty"`
	Monitor  *int   `json:"monitor,omitempty"`
	PID      int    `json:"pid,omitempty"`
	WindowID string `json:"window_id,omitempty"`

	// file
	Kind string `json:"kind,omitempty"`
	Path string `json:"path,omitempty"`
	Size *int64 `json:"size,omitempty"`
	Dir  bool   `json:"dir,omitempty"`
}

// tracker reports transitions in a sequence of observations. The first sample
// is the state the caller was already in, not news, so it only establishes the
// baseline — that is what makes `--max=1` mean "wait for the next change".
type tracker struct {
	started bool
	last    string
}

func (t *tracker) changed(key string) bool {
	if !t.started {
		t.started, t.last = true, key
		return false
	}
	if key == t.last {
		return false
	}
	t.last = key
	return true
}

// parseInterval accepts a Go duration (`500ms`, `2s`) and a bare number, which
// is read as milliseconds to match `msg listen --interval`.
func parseInterval(value string, fallback time.Duration) (time.Duration, error) {
	if value == "" {
		return fallback, nil
	}
	if ms, err := strconv.Atoi(value); err == nil {
		return time.Duration(ms) * time.Millisecond, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("--interval must be a duration like 500ms, got %q", value)
	}
	return d, nil
}

// clipboardEvent describes a clipboard change. The text is only included when
// the caller asks for it: clipboards routinely hold passwords, tokens and
// whatever the last `pbcopy` swept up, and a watcher left running in a log or
// piped to an agent would capture all of it. Length plus a short digest is
// enough to see *that* it changed and to correlate two events, which is what a
// watcher is for.
func clipboardEvent(at time.Time, text string, withContent bool) watchEvent {
	length := len(text)
	event := watchEvent{
		Event:  "clipboard",
		At:     at.Format(time.RFC3339),
		Length: &length,
		Digest: digest(text),
	}
	if withContent {
		event.Text = text
	}
	return event
}

// windowEvent describes a focus change. It carries what identifies the window
// to a person and to a follow-up command — app, title, monitor — not its
// geometry, which changes for reasons that are not a focus change.
func windowEvent(at time.Time, w engine.Window) watchEvent {
	monitor := w.Monitor
	return watchEvent{
		Event:    "window",
		At:       at.Format(time.RFC3339),
		App:      w.App,
		Title:    w.Title,
		Monitor:  &monitor,
		PID:      w.PID,
		WindowID: w.ID,
	}
}

// digest fingerprints clipboard content so two events can be compared without
// the content itself travelling with them. Truncated because it identifies a
// change, and is not meant to be checked against anything.
func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:12]
}

// focusKey is the identity a focus change is measured against. The window id is
// not enough on its own: a browser tab switch keeps the id and changes only the
// title, and that is a change worth reporting.
func focusKey(w engine.Window) string {
	return strings.Join([]string{w.ID, w.App, w.Title, strconv.Itoa(w.Monitor)}, "\x00")
}

func runWatchClipboard(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "interval", "max")
	if err != nil {
		return err
	}
	// --content is a bare flag, so it stays out of parseArgs' value list.
	if err := set.Reject("interval", "max", "content"); err != nil {
		return err
	}
	interval, max, err := watchOptions(set)
	if err != nil {
		return err
	}
	withContent := set.Has("content")

	return watchLoop(c, interval, max, func(at time.Time) (string, watchEvent, error) {
		text, err := clipboardRead()
		if err != nil {
			return "", watchEvent{}, err
		}
		return text, clipboardEvent(at, text, withContent), nil
	})
}

func runWatchWindow(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "interval", "max")
	if err != nil {
		return err
	}
	if err := set.Reject("interval", "max"); err != nil {
		return err
	}
	interval, max, err := watchOptions(set)
	if err != nil {
		return err
	}

	return watchLoop(c, interval, max, func(at time.Time) (string, watchEvent, error) {
		window, err := focusedWindow()
		if err != nil {
			return "", watchEvent{}, err
		}
		return focusKey(window), windowEvent(at, window), nil
	})
}

// focusedWindow returns the window the desktop currently considers frontmost.
func focusedWindow() (engine.Window, error) {
	windows, err := engine.ListWindows(engine.Filter{})
	if err != nil {
		return engine.Window{}, err
	}
	for _, w := range windows {
		if w.Focused {
			return w, nil
		}
	}
	return engine.Window{}, fmt.Errorf("no focused window")
}

// watchOptions reads the two flags every watcher shares.
func watchOptions(set *argSet) (time.Duration, int, error) {
	interval, err := parseInterval(set.String("interval", ""), defaultWatchInterval)
	if err != nil {
		return 0, 0, err
	}
	if interval < minWatchInterval {
		return 0, 0, fmt.Errorf("--interval must be at least %s", minWatchInterval)
	}
	max, err := set.Int("max", 0)
	if err != nil {
		return 0, 0, err
	}
	if max < 0 {
		return 0, 0, fmt.Errorf("--max must not be negative")
	}
	return interval, max, nil
}

// watchLoop polls, prints an event whenever poll's key changes, and stops after
// max events (0 meaning never) or on Ctrl-C. Everything a watcher differs in
// lives in poll, so the two commands cannot drift apart in how they end, how
// they tick, or how they handle a failing sample.
func watchLoop(c *cli.Ctx, interval time.Duration, max int, poll func(time.Time) (string, watchEvent, error)) error {
	// Ctrl-C should end the loop cleanly rather than killing it mid-write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seen tracker
	seq := 0
	for {
		key, event, err := poll(time.Now())
		if err != nil {
			// A window that is briefly unfocused, or a clipboard holding
			// something the platform tool cannot read as text, is not a reason
			// to end a long-running watcher; report and try the next tick.
			c.Warnf("aos: %v\n", err)
		} else if seen.changed(key) {
			seq++
			event.Seq = seq
			line, err := json.Marshal(event)
			if err != nil {
				return err
			}
			c.Printf("%s\n", line)
			if max > 0 && seq >= max {
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// fileEntry is what a poll remembers about one path. Content is deliberately
// not read: a watcher that hashed every file would turn a directory of large
// files into an I/O storm every tick, and modtime plus size is what a
// filesystem already maintains for exactly this question.
type fileEntry struct {
	ModTime time.Time
	Size    int64
	IsDir   bool
}

// fileChange is one reportable difference between two snapshots.
type fileChange struct {
	Kind string // created | modified | deleted
	Path string
	Size int64
	Dir  bool
}

// diffFileSnapshots reports what changed between two snapshots, in path order
// so a tick that touches several files always reads the same way. Directories
// are compared on existence alone: a directory's modtime changes whenever any
// child is added or removed, so tracking it would report the parent as
// "modified" alongside every create and delete inside it.
func diffFileSnapshots(prev, cur map[string]fileEntry) []fileChange {
	var changes []fileChange
	for path, now := range cur {
		before, existed := prev[path]
		switch {
		case !existed:
			changes = append(changes, fileChange{Kind: "created", Path: path, Size: now.Size, Dir: now.IsDir})
		case before.IsDir != now.IsDir:
			// A file replaced by a directory of the same name, or the reverse.
			changes = append(changes, fileChange{Kind: "modified", Path: path, Size: now.Size, Dir: now.IsDir})
		case !now.IsDir && (!before.ModTime.Equal(now.ModTime) || before.Size != now.Size):
			changes = append(changes, fileChange{Kind: "modified", Path: path, Size: now.Size, Dir: now.IsDir})
		}
	}
	for path, before := range prev {
		if _, still := cur[path]; !still {
			changes = append(changes, fileChange{Kind: "deleted", Path: path, Dir: before.IsDir})
		}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}

// snapshotPath samples the watched path. A root that has gone missing is an
// empty snapshot rather than an error, so its disappearance is reported as a
// deletion and the watcher keeps running in case it comes back.
func snapshotPath(root string, recursive bool) (map[string]fileEntry, error) {
	snap := map[string]fileEntry{}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return snap, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		snap[root] = entryOf(info)
		return snap, nil
	}
	if !recursive {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				return snap, nil
			}
			return nil, err
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil {
				// Removed between the listing and the stat: it is simply not
				// there this tick, which the diff already reports.
				continue
			}
			snap[filepath.Join(root, e.Name())] = entryOf(info)
		}
		return snap, nil
	}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil // a child that vanished mid-walk is a deletion, not a failure
		}
		if path == root {
			return nil // the root itself is the subject, not an entry in it
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		snap[path] = entryOf(info)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]fileEntry{}, nil
		}
		return nil, err
	}
	return snap, nil
}

func entryOf(info os.FileInfo) fileEntry {
	return fileEntry{ModTime: info.ModTime(), Size: info.Size(), IsDir: info.IsDir()}
}

// fileEvent describes one filesystem change in the shared watch envelope. A
// deleted entry has no size worth reporting, so the field is omitted rather
// than sent as 0.
func fileEvent(at time.Time, change fileChange) watchEvent {
	event := watchEvent{
		Event: "file",
		At:    at.Format(time.RFC3339),
		Kind:  change.Kind,
		Path:  change.Path,
		Dir:   change.Dir,
	}
	if change.Kind != "deleted" && !change.Dir {
		size := change.Size
		event.Size = &size
	}
	return event
}

func runWatchFile(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "interval", "max")
	if err != nil {
		return err
	}
	// --recursive is a bare flag, so it stays out of parseArgs' value list.
	if err := set.Reject("interval", "max", "recursive"); err != nil {
		return err
	}
	interval, max, err := watchOptions(set)
	if err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return &cli.ExitError{Code: 1, Message: "`watch file` takes exactly one path"}
	}
	root := set.Rest[0]
	recursive := set.Has("recursive")

	// A path that is not there at the start is a mistake in the command, not a
	// change to report — fail rather than silently watch nothing.
	if _, err := os.Stat(root); err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("cannot watch %s: %v", root, err)}
	}

	return watchFileLoop(c, interval, max, root, recursive)
}

// watchFileLoop is watchLoop's shape for a poll that can produce several
// events at once: one tick of a directory may create, modify and delete
// different entries, and each of those is its own line and its own seq.
func watchFileLoop(c *cli.Ctx, interval time.Duration, max int, root string, recursive bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// The first sample is the state the caller was already in, not news, so it
	// only establishes the baseline — that is what makes `--max=1` mean "wait
	// for the next change".
	prev, err := snapshotPath(root, recursive)
	if err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("cannot watch %s: %v", root, err)}
	}

	seq := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}

		cur, err := snapshotPath(root, recursive)
		if err != nil {
			// A directory that is briefly unreadable is not a reason to end a
			// long-running watcher; report and try the next tick.
			c.Warnf("aos: %v\n", err)
			continue
		}
		for _, change := range diffFileSnapshots(prev, cur) {
			seq++
			event := fileEvent(time.Now(), change)
			event.Seq = seq
			line, err := json.Marshal(event)
			if err != nil {
				return err
			}
			c.Printf("%s\n", line)
			if max > 0 && seq >= max {
				return nil
			}
		}
		prev = cur
	}
}
