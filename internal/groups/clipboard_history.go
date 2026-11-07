package groups

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// Clipboard history. No OS offers a usable history API, so aos keeps its own:
// an opt-in recorder (`clipboard history start`) runs detached, polls the
// clipboard like `watch clipboard`, and appends each new text to a JSON Lines
// file. Only what the recorder saw while it ran is in the history.

const (
	historyMaxEntries = 200
	historyMaxAge     = 7 * 24 * time.Hour
	historyMaxBytes   = 64 * 1024
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "clipboard", Name: "history",
				Summary: "List the texts the history recorder saw, newest first",
				Args:    "[--limit=<n>] [--json]",
				Examples: []string{
					"aos clipboard history",
					"aos clipboard history --limit=5 --json",
				},
				Run: runClipboardHistory,
			},
			&cli.Command{
				Group: "clipboard", Name: "history start",
				Summary:  "Start recording clipboard history in the background (opt-in; ask the person first)",
				Examples: []string{"aos clipboard history start"},
				Run:      runClipboardHistoryStart,
			},
			&cli.Command{
				Group: "clipboard", Name: "history stop",
				Summary:  "Stop the clipboard history recorder (the history is kept)",
				Examples: []string{"aos clipboard history stop"},
				Run:      runClipboardHistoryStop,
			},
			&cli.Command{
				Group: "clipboard", Name: "history status",
				Summary:  "Say whether the history recorder is running and how much it holds",
				Args:     "[--json]",
				Examples: []string{"aos clipboard history status"},
				Run:      runClipboardHistoryStatus,
			},
			&cli.Command{
				Group: "clipboard", Name: "history get",
				Summary:  "Print history entry <n> (1 is the newest)",
				Args:     "<n>",
				Examples: []string{"aos clipboard history get 2"},
				Run:      runClipboardHistoryGet,
			},
			&cli.Command{
				Group: "clipboard", Name: "history restore",
				Summary:  "Put history entry <n> back on the clipboard (1 is the newest)",
				Args:     "<n>",
				Examples: []string{"aos clipboard history restore 2"},
				Run:      runClipboardHistoryRestore,
			},
			&cli.Command{
				Group: "clipboard", Name: "history clear",
				Summary:  "Delete the recorded clipboard history",
				Examples: []string{"aos clipboard history clear"},
				Run:      runClipboardHistoryClear,
			},
			&cli.Command{
				Group: "clipboard", Name: "history record",
				Summary:  "Run the history recorder in the foreground (started by `history start`)",
				Hidden:   true,
				Blocking: true,
				Examples: []string{"aos clipboard history record"},
				Run:      runClipboardHistoryRecord,
			},
		)
	})
}

// historyEntry is one line of the history file.
type historyEntry struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// historyStore is the JSON Lines file, oldest entry first.
type historyStore struct {
	Path       string
	MaxEntries int
	MaxAge     time.Duration
}

func clipboardHistoryDir(c *cli.Ctx) string {
	return filepath.Join(cli.ConfigDir(c.Env), "clipboard")
}

func newHistoryStore(dir string) *historyStore {
	return &historyStore{Path: filepath.Join(dir, "history.jsonl"), MaxEntries: historyMaxEntries, MaxAge: historyMaxAge}
}

// Load returns the entries still within the limits, oldest first. A missing
// file is an empty history; an unreadable line is skipped.
func (s *historyStore) Load(now time.Time) ([]historyEntry, error) {
	data, err := os.ReadFile(s.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []historyEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 4*historyMaxBytes)
	for sc.Scan() {
		var e historyEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			entries = append(entries, e)
		}
	}
	return s.prune(entries, now), nil
}

func (s *historyStore) prune(entries []historyEntry, now time.Time) []historyEntry {
	kept := entries[:0:0]
	for _, e := range entries {
		if s.MaxAge > 0 && now.Sub(e.At) > s.MaxAge {
			continue
		}
		kept = append(kept, e)
	}
	if s.MaxEntries > 0 && len(kept) > s.MaxEntries {
		kept = kept[len(kept)-s.MaxEntries:]
	}
	return kept
}

// Add records text unless it is empty, too large, or the same as the newest
// entry. It reports whether the text was recorded.
func (s *historyStore) Add(text string, now time.Time) (bool, error) {
	if strings.TrimSpace(text) == "" || len(text) > historyMaxBytes {
		return false, nil
	}
	entries, err := s.Load(now)
	if err != nil {
		return false, err
	}
	if n := len(entries); n > 0 && entries[n-1].Text == text {
		return false, nil
	}
	entries = s.prune(append(entries, historyEntry{At: now.UTC(), Text: text}), now)
	return true, s.write(entries)
}

// write replaces the file atomically so a reader, or a recorder killed
// mid-write, never sees half a history. The file is private: 0600.
func (s *historyStore) write(entries []historyEntry) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".history-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}

// Clear deletes the history.
func (s *historyStore) Clear() error {
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// newest returns entry n counted from the newest (1-based).
func newest(entries []historyEntry, n int) (historyEntry, error) {
	if n < 1 || n > len(entries) {
		return historyEntry{}, fmt.Errorf("no history entry %d (the history holds %d)", n, len(entries))
	}
	return entries[len(entries)-n], nil
}

// concealedTypes reports whether a list of clipboard type names (one per line)
// holds a marker a password manager sets to keep its item out of histories.
func concealedTypes(types string) bool {
	for _, t := range strings.Split(types, "\n") {
		switch strings.TrimSpace(t) {
		case "org.nspasteboard.ConcealedType", "org.nspasteboard.TransientType", "x-kde-passwordManagerHint":
			return true
		}
	}
	return false
}

// ---- recorder ---------------------------------------------------------------

func historyPIDPath(dir string) string { return filepath.Join(dir, "recorder.pid") }

// recorderPID returns the running recorder's pid, or 0.
func recorderPID(dir string) int {
	data, err := os.ReadFile(historyPIDPath(dir))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || !videoProcAlive(pid) {
		return 0
	}
	return pid
}

func runClipboardHistoryStart(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	dir := clipboardHistoryDir(c)
	if pid := recorderPID(dir); pid != 0 {
		c.Printf("clipboard history recorder already running (pid %d)\n", pid)
		return nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "recorder.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(self, "clipboard", "history", "record")
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.Env = append(os.Environ(), "AOS_CONFIG_DIR="+cli.ConfigDir(c.Env))
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(historyPIDPath(dir), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	_ = cmd.Process.Release()
	c.Printf("clipboard history recorder started (pid %d); history in %s\n", pid, newHistoryStore(dir).Path)
	return nil
}

func runClipboardHistoryStop(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	dir := clipboardHistoryDir(c)
	pid := recorderPID(dir)
	_ = os.Remove(historyPIDPath(dir))
	if pid == 0 {
		c.Println("clipboard history recorder is not running")
		return nil
	}
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
	c.Printf("clipboard history recorder stopped (pid %d)\n", pid)
	return nil
}

func runClipboardHistoryStatus(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	dir := clipboardHistoryDir(c)
	store := newHistoryStore(dir)
	entries, err := store.Load(time.Now())
	if err != nil {
		return err
	}
	pid := recorderPID(dir)
	if set.Has("json") {
		return printJSONValue(c, map[string]any{"running": pid != 0, "pid": pid, "entries": len(entries), "path": store.Path})
	}
	if pid != 0 {
		c.Printf("recording (pid %d), %d entries in %s\n", pid, len(entries), store.Path)
	} else {
		c.Printf("not recording, %d entries in %s\n", len(entries), store.Path)
	}
	return nil
}

// runClipboardHistoryRecord is the detached recorder. It exits when the pid
// file no longer names it, so a stale or replaced recorder does not linger.
func runClipboardHistoryRecord(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	dir := clipboardHistoryDir(c)
	store := newHistoryStore(dir)
	me := strconv.Itoa(os.Getpid())
	var seen tracker
	for {
		data, err := os.ReadFile(historyPIDPath(dir))
		if err != nil || strings.TrimSpace(string(data)) != me {
			return nil
		}
		if text, err := clipboardRead(); err == nil && seen.changed(text) {
			if !clipboardConcealed() {
				if _, err := store.Add(text, time.Now()); err != nil {
					fmt.Fprintln(c.Stderr, "clipboard history:", err)
				}
			}
		}
		time.Sleep(defaultWatchInterval)
	}
}

// ---- reading ----------------------------------------------------------------

func runClipboardHistory(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "limit")
	if err != nil {
		return err
	}
	if err := set.Reject("limit", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("unknown history command %q\nTry: aos clipboard --help", set.Rest[0])
	}
	limit, err := set.Int("limit", 20)
	if err != nil {
		return err
	}
	entries, err := newHistoryStore(clipboardHistoryDir(c)).Load(time.Now())
	if err != nil {
		return err
	}
	type row struct {
		N    int       `json:"n"`
		At   time.Time `json:"at"`
		Text string    `json:"text"`
	}
	var rows []row
	for n := 1; n <= len(entries) && (limit <= 0 || n <= limit); n++ {
		e := entries[len(entries)-n]
		rows = append(rows, row{N: n, At: e.At, Text: e.Text})
	}
	if set.Has("json") {
		if rows == nil {
			rows = []row{}
		}
		return printJSONValue(c, rows)
	}
	if len(rows) == 0 {
		c.Println("clipboard history is empty (start recording with: aos clipboard history start)")
		return nil
	}
	for _, r := range rows {
		c.Printf("%3d  %s  %s\n", r.N, r.At.Local().Format("2006-01-02 15:04"), preview(r.Text, 70))
	}
	return nil
}

// preview flattens text to one line and shortens it for the list view.
func preview(text string, max int) string {
	one := strings.Join(strings.Fields(text), " ")
	if r := []rune(one); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return one
}

func historyEntryArg(c *cli.Ctx, args []string) (historyEntry, error) {
	if len(args) != 1 {
		return historyEntry{}, fmt.Errorf("need exactly one entry number (1 is the newest)")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil {
		return historyEntry{}, fmt.Errorf("entry must be a number, got %q", args[0])
	}
	entries, err := newHistoryStore(clipboardHistoryDir(c)).Load(time.Now())
	if err != nil {
		return historyEntry{}, err
	}
	return newest(entries, n)
}

func runClipboardHistoryGet(c *cli.Ctx, args []string) error {
	e, err := historyEntryArg(c, args)
	if err != nil {
		return err
	}
	c.Println(e.Text)
	return nil
}

func runClipboardHistoryRestore(c *cli.Ctx, args []string) error {
	e, err := historyEntryArg(c, args)
	if err != nil {
		return err
	}
	if err := clipboardWrite(e.Text); err != nil {
		return err
	}
	c.Printf("restored %d characters to the clipboard\n", len([]rune(e.Text)))
	return nil
}

func runClipboardHistoryClear(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unexpected argument %q", args[0])
	}
	if err := newHistoryStore(clipboardHistoryDir(c)).Clear(); err != nil {
		return err
	}
	c.Println("clipboard history cleared")
	return nil
}

func printJSONValue(c *cli.Ctx, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	c.Println(string(data))
	return nil
}
