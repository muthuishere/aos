package groups

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// topEntry is one ranked row of `disk top`. Bytes is the size of the whole
// subtree (allocated where the OS gives it, see topReport.SizeKind). Tag,
// Note and Safe are set for well-known places so an agent can explain them:
// Safe is "yes" (regenerated, or meant to be emptied), "review" (personal
// data, look first), "tool" (clean through the owning tool) or "no".
type topEntry struct {
	Path  string `json:"path"`
	Name  string `json:"name"`
	Dir   bool   `json:"dir"`
	Bytes int64  `json:"bytes"`
	Depth int    `json:"depth"`
	Tag   string `json:"tag,omitempty"`
	Note  string `json:"note,omitempty"`
	Safe  string `json:"safe,omitempty"`
}

type topReport struct {
	Root       string     `json:"root"`
	TotalBytes int64      `json:"total_bytes"`
	SizeKind   string     `json:"size_kind"`
	Partial    bool       `json:"partial"`
	Unreadable int64      `json:"unreadable"`
	ElapsedMS  int64      `json:"elapsed_ms"`
	Entries    []topEntry `json:"entries"`
}

type topOptions struct {
	Depth   int
	Limit   int
	Files   bool
	Workers int
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "disk", Name: "top",
			Summary: "Rank the biggest folders (and files) under a path, tagging caches, trash and other known space hogs",
			Args:    "[<path>] [--depth=<n>] [--limit=<n>] [--timeout=<dur>] [--files] [--json]",
			Examples: []string{
				"aos disk top                      # biggest folders in your home, 5 s budget",
				"aos disk top ~/Library --depth=2 --limit=30",
				"aos disk top ~/Downloads --files --json",
				"aos disk top / --timeout=30s --json",
			},
			Run: runDiskTop,
		})
	})
}

func runDiskTop(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "depth", "limit", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("depth", "limit", "timeout", "files", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 1 {
		return fmt.Errorf("`disk top` takes at most one path")
	}
	opt := topOptions{Files: set.Has("files"), Workers: runtime.NumCPU() * 2}
	if opt.Depth, err = set.Int("depth", 1); err != nil {
		return err
	}
	if opt.Limit, err = set.Int("limit", 20); err != nil {
		return err
	}
	if opt.Depth < 1 || opt.Limit < 1 {
		return fmt.Errorf("--depth and --limit must be at least 1")
	}
	timeout, err := time.ParseDuration(set.String("timeout", "5s"))
	if err != nil || timeout <= 0 {
		return fmt.Errorf("--timeout must be a duration like 5s or 1m")
	}
	root := ""
	if len(set.Rest) == 1 {
		root = expandHome(set.Rest[0])
	} else if root, err = os.UserHomeDir(); err != nil {
		return err
	}
	if root, err = filepath.Abs(root); err != nil {
		return err
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("not a directory: %s", root)}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	rep := diskTop(ctx, root, opt, knownPlaces())

	if set.Has("json") {
		return writeJSON(c, rep)
	}
	for _, e := range rep.Entries {
		name := e.Path
		if e.Dir {
			name += string(filepath.Separator)
		}
		line := fmt.Sprintf("%10s  %s", humanBytes(uint64(e.Bytes)), name)
		if e.Tag != "" {
			line += fmt.Sprintf("  [%s, safe to clean: %s]", e.Tag, e.Safe)
		}
		c.Println(line)
	}
	tail := fmt.Sprintf("%s total under %s in %.1fs", humanBytes(uint64(rep.TotalBytes)), rep.Root, float64(rep.ElapsedMS)/1000)
	if rep.Partial {
		tail += "; PARTIAL: the walk hit --timeout, sizes are lower bounds"
	}
	if rep.Unreadable > 0 {
		tail += fmt.Sprintf("; %d folder(s) unreadable", rep.Unreadable)
	}
	fmt.Fprintln(c.Stderr, tail)
	return nil
}

// topNode is a folder or file shown as a row. own collects the bytes under
// it that do not belong to a deeper row; totals are summed after the walk.
type topNode struct {
	entry    topEntry
	own      atomic.Int64
	mu       sync.Mutex
	children []*topNode
}

func (n *topNode) add(child *topNode) {
	n.mu.Lock()
	n.children = append(n.children, child)
	n.mu.Unlock()
}

// kids copies the children: a walker stuck past the deadline may still add.
func (n *topNode) kids() []*topNode {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]*topNode(nil), n.children...)
}

func (n *topNode) total() int64 {
	sum := n.own.Load()
	for _, ch := range n.kids() {
		sum += ch.total()
	}
	n.entry.Bytes = sum
	return sum
}

// diskTop walks root in parallel, at most opt.Workers ReadDir calls at once,
// until ctx ends, and then returns at once with a partial report. It never
// follows symlinks and does not cross into other volumes. Folders, and files with opt.Files, down
// to opt.Depth become rows; deeper content is summed into its nearest row.
func diskTop(ctx context.Context, root string, opt topOptions, places map[string]place) topReport {
	start := time.Now()
	if opt.Workers < 1 {
		opt.Workers = 1
	}
	rootNode := &topNode{entry: topEntry{Path: root, Dir: true}}
	_, rootDev := entrySize(root, nil)
	sem := make(chan struct{}, opt.Workers)
	var (
		wg         sync.WaitGroup
		partial    atomic.Bool
		unreadable atomic.Int64
	)
	var visit func(dir string, depth int, acc *topNode)
	visit = func(dir string, depth int, acc *topNode) {
		defer wg.Done()
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			partial.Store(true)
			return
		}
		entries, err := os.ReadDir(dir)
		<-sem
		if err != nil {
			unreadable.Add(1)
			return
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				partial.Store(true)
				return
			}
			path := filepath.Join(dir, e.Name())
			size, dev := entrySize(path, e)
			if e.IsDir() && dev != rootDev {
				continue // another volume mounted here
			}
			target := acc
			if depth <= opt.Depth && (e.IsDir() || opt.Files) {
				n := &topNode{entry: topEntry{Path: path, Name: e.Name(), Dir: e.IsDir(), Depth: depth}}
				tagEntry(&n.entry, places)
				acc.add(n)
				target = n
			}
			target.own.Add(size)
			if e.IsDir() {
				wg.Add(1)
				go visit(path, depth+1, target)
			}
		}
	}
	wg.Add(1)
	go visit(root, 1, rootNode)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		// A ReadDir or stat stuck on a slow or cloud-backed folder cannot be
		// interrupted; give the walkers a moment, then report what is in.
		select {
		case <-done:
		case <-time.After(250 * time.Millisecond):
			partial.Store(true)
		}
	}

	rep := topReport{
		Root: root, TotalBytes: rootNode.total(), SizeKind: sizeKind,
		Partial: partial.Load() || ctx.Err() != nil, Unreadable: unreadable.Load(),
		ElapsedMS: time.Since(start).Milliseconds(), Entries: []topEntry{},
	}
	var flatten func(n *topNode)
	flatten = func(n *topNode) {
		for _, ch := range n.kids() {
			rep.Entries = append(rep.Entries, ch.entry)
			flatten(ch)
		}
	}
	flatten(rootNode)
	sort.SliceStable(rep.Entries, func(i, j int) bool {
		if rep.Entries[i].Bytes != rep.Entries[j].Bytes {
			return rep.Entries[i].Bytes > rep.Entries[j].Bytes
		}
		return rep.Entries[i].Path < rep.Entries[j].Path
	})
	if len(rep.Entries) > opt.Limit {
		rep.Entries = rep.Entries[:opt.Limit]
	}
	return rep
}

// place describes a well-known folder.
type place struct{ Tag, Note, Safe string }

var nodeModulesPlace = place{"node_modules", "npm/yarn/pnpm dependencies; `npm install` brings them back", "yes"}

// knownPlaces maps well-known folders (by placeKey) to what they are.
func knownPlaces() map[string]place {
	home, _ := os.UserHomeDir()
	m := map[string]place{}
	put := func(p place, paths ...string) {
		for _, path := range paths {
			if path != "" && filepath.IsAbs(path) {
				m[placeKey(path)] = p
			}
		}
	}
	under := func(base string, parts ...string) string {
		if base == "" {
			return ""
		}
		return filepath.Join(append([]string{base}, parts...)...)
	}
	h := func(parts ...string) string { return under(home, parts...) }
	env := func(name string, parts ...string) string { return under(os.Getenv(name), parts...) }

	put(place{"temp", "temporary files; safe once the apps that made them have exited", "yes"}, os.TempDir())
	put(place{"trash", "deleted files waiting in the Trash / Recycle Bin; emptying is permanent", "yes"},
		h(".Trash"), h(".local", "share", "Trash"), `C:\$Recycle.Bin`)
	put(place{"downloads", "files you downloaded, often old installers and archives", "review"}, h("Downloads"))
	put(place{"cache", "app caches; apps rebuild them (quit the apps first)", "yes"},
		h("Library", "Caches"), h(".cache"), env("TEMP"), env("LOCALAPPDATA", "Temp"))
	put(place{"xcode-derived-data", "Xcode build products; Xcode rebuilds them", "yes"},
		h("Library", "Developer", "Xcode", "DerivedData"))
	put(place{"docker", "Docker images, containers and volumes; clean with `docker system prune`, not by deleting files", "tool"},
		h("Library", "Containers", "com.docker.docker"), "/var/lib/docker",
		env("LOCALAPPDATA", "Docker"), env("ProgramData", "DockerDesktop"))
	put(place{"ios-backup", "iPhone/iPad backups; remove old ones from Finder or iTunes after checking", "review"},
		h("Library", "Application Support", "MobileSync", "Backup"), env("APPDATA", "Apple Computer", "MobileSync", "Backup"))
	put(place{"spotlight-index", "the Spotlight search index; macOS manages it, do not delete", "no"},
		"/.Spotlight-V100", "/System/Volumes/Data/.Spotlight-V100")
	return m
}

func placeKey(path string) string { return strings.ToLower(filepath.Clean(path)) }

func tagEntry(e *topEntry, places map[string]place) {
	p, ok := places[placeKey(e.Path)]
	if !ok && e.Dir && e.Name == "node_modules" {
		p, ok = nodeModulesPlace, true
	}
	if ok {
		e.Tag, e.Note, e.Safe = p.Tag, p.Note, p.Safe
	}
}
