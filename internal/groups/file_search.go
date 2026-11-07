package groups

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/muthuishere/aos/internal/cli"
)

// searchHit is one `file search` result. Source names the engine that
// answered (mdfind, plocate, locate, fd, everything, walk), so a caller can
// tell an instant index answer from a bounded walk.
type searchHit struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Dir    bool   `json:"dir"`
	Source string `json:"source"`
}

// searchQuery is what every engine is handed.
type searchQuery struct {
	Pattern string // as typed; substring, or a glob when it holds * or ?
	Root    string // "" means the engine's default scope
	Kind    string // "", "file" or "dir"
	Limit   int
}

// searchEngine is one way of answering a query. Run returns
// errEngineUnavailable when its tool is not installed.
type searchEngine struct {
	Name string
	Run  func(ctx context.Context, q searchQuery) ([]searchHit, error)
}

var errEngineUnavailable = errors.New("engine not available on this machine")

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "file", Name: "search",
			Summary: "Find files and folders by name, through the OS index when there is one",
			Args:    "<query> [--root=<dir>] [--kind=file|dir] [--limit=<n>] [--engine=auto|everything|index|walk] [--json]",
			Examples: []string{
				"aos file search invoice --limit=10",
				`aos file search "*.pdf" --root=~/Downloads --json`,
				"aos file search notes --kind=dir --engine=walk",
			},
			Run: runFileSearch,
		})
	})
}

func runFileSearch(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "root", "kind", "limit", "engine")
	if err != nil {
		return err
	}
	if err := set.Reject("root", "kind", "limit", "engine", "json"); err != nil {
		return err
	}
	if len(set.Rest) != 1 || strings.TrimSpace(set.Rest[0]) == "" {
		return fmt.Errorf("`file search` takes one query, e.g. aos file search invoice")
	}
	q := searchQuery{Pattern: set.Rest[0], Kind: set.String("kind", "")}
	if q.Kind != "" && q.Kind != "file" && q.Kind != "dir" {
		return fmt.Errorf("--kind must be file or dir, got %q", q.Kind)
	}
	if q.Limit, err = set.Int("limit", 50); err != nil {
		return err
	}
	if q.Limit <= 0 {
		return fmt.Errorf("--limit must be at least 1")
	}
	if root := set.String("root", ""); root != "" {
		abs, err := filepath.Abs(expandHome(root))
		if err != nil {
			return err
		}
		if info, err := os.Stat(abs); err != nil || !info.IsDir() {
			return fmt.Errorf("--root %q is not a directory", root)
		}
		q.Root = abs
	}

	engines, err := selectEngines(set.String("engine", "auto"), platformSearchEngines())
	if err != nil {
		return err
	}
	hits, err := runSearch(context.Background(), engines, q)
	if err != nil {
		return err
	}

	if set.Has("json") {
		if hits == nil {
			hits = []searchHit{}
		}
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(hits)
	}
	for _, h := range hits {
		c.Printf("%s\n", h.Path)
	}
	source := engines[len(engines)-1].Name
	if len(hits) > 0 {
		source = hits[0].Source
	}
	fmt.Fprintf(c.Stderr, "%d result(s) from %s\n", len(hits), source)
	return nil
}

// selectEngines turns --engine into the ordered list to try. "auto" is the
// platform's list (indexes first) followed by the walk; "index" is the
// platform's indexes without Everything or the walk; "everything" is
// Everything alone.
func selectEngines(choice string, platform []searchEngine) ([]searchEngine, error) {
	walk := searchEngine{Name: "walk", Run: walkSearch}
	switch choice {
	case "", "auto":
		return append(append([]searchEngine{}, platform...), walk), nil
	case "walk":
		return []searchEngine{walk}, nil
	case "index":
		var out []searchEngine
		for _, e := range platform {
			if e.Name != "everything" {
				out = append(out, e)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no search index engine on this OS; use --engine=walk")
		}
		return out, nil
	case "everything":
		for _, e := range platform {
			if e.Name == "everything" {
				return []searchEngine{e}, nil
			}
		}
		return nil, fmt.Errorf("--engine=everything is Windows only")
	}
	return nil, fmt.Errorf("--engine must be auto, everything, index or walk, got %q", choice)
}

// runSearch tries the engines in order. An engine that is missing, fails, or
// finds nothing hands over to the next one (an index that is switched off
// answers with nothing rather than an error, so an empty answer is not
// trusted while a fallback remains). The last engine's answer stands.
func runSearch(ctx context.Context, engines []searchEngine, q searchQuery) ([]searchHit, error) {
	var lastErr error
	for i, e := range engines {
		hits, err := e.Run(ctx, q)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", e.Name, err)
			continue
		}
		if len(hits) > 0 || i == len(engines)-1 {
			return hits, nil
		}
		lastErr = nil
	}
	return nil, lastErr
}

// nameMatcher returns the test a base name must pass: a case-insensitive
// glob when the pattern holds * or ?, otherwise a case-insensitive substring.
func nameMatcher(pattern string) func(name string) bool {
	p := strings.ToLower(pattern)
	if strings.ContainsAny(p, "*?") {
		return func(name string) bool {
			ok, _ := filepath.Match(p, strings.ToLower(name))
			return ok
		}
	}
	return func(name string) bool { return strings.Contains(strings.ToLower(name), p) }
}

// literalCore is the longest wildcard-free run of a pattern: what an index
// that only does substrings is asked for before nameMatcher filters exactly.
func literalCore(pattern string) string {
	best := ""
	for _, part := range strings.FieldsFunc(pattern, func(r rune) bool { return r == '*' || r == '?' }) {
		if len(part) > len(best) {
			best = part
		}
	}
	return best
}

// skipDir reports whether the walk leaves a directory alone: dependency and
// VCS trees, the recycle bin, and Windows' component store.
func skipDir(path, name string) bool {
	switch strings.ToLower(name) {
	case "node_modules", ".git", "$recycle.bin", "system volume information":
		return true
	case "winsxs":
		return strings.EqualFold(filepath.Base(filepath.Dir(path)), "windows")
	}
	return false
}

// hitCollector filters index output by name, root, kind and limit, so every
// engine answers the same question the walk does.
type hitCollector struct {
	q     searchQuery
	match func(string) bool
	src   string
	hits  []searchHit
	seen  map[string]bool
}

func newCollector(q searchQuery, source string) *hitCollector {
	return &hitCollector{q: q, match: nameMatcher(q.Pattern), src: source, seen: map[string]bool{}}
}

// add offers one path; it returns false once the limit is reached.
func (h *hitCollector) add(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || h.seen[path] {
		return len(h.hits) < h.q.Limit
	}
	name := filepath.Base(path)
	if !h.match(name) || !underRoot(path, h.q.Root) {
		return true
	}
	info, err := os.Lstat(path)
	if err != nil {
		return true // stale index entry
	}
	if (h.q.Kind == "file" && info.IsDir()) || (h.q.Kind == "dir" && !info.IsDir()) {
		return true
	}
	h.seen[path] = true
	h.hits = append(h.hits, searchHit{Path: path, Name: name, Dir: info.IsDir(), Source: h.src})
	return len(h.hits) < h.q.Limit
}

func underRoot(path, root string) bool {
	if root == "" {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// streamCommand runs a tool and feeds each stdout line to each, stopping the
// tool as soon as each returns false.
func streamCommand(ctx context.Context, each func(line string) bool, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return errEngineUnavailable
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	hideChild(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	stopped := false
	for sc.Scan() {
		if !each(sc.Text()) {
			stopped = true
			cancel()
			break
		}
	}
	err = cmd.Wait()
	if stopped {
		return nil
	}
	return err
}

// indexEngine wraps a command-line index: build its argv, collect its lines.
func indexEngine(source, tool string, argv func(q searchQuery) []string) searchEngine {
	return searchEngine{Name: source, Run: func(ctx context.Context, q searchQuery) ([]searchHit, error) {
		col := newCollector(q, source)
		if err := streamCommand(ctx, col.add, tool, argv(q)...); err != nil && len(col.hits) == 0 {
			return nil, err
		}
		return col.hits, nil
	}}
}

// walkSearch is the parallel walk every OS falls back to. Its default root
// is the user's home.
func walkSearch(ctx context.Context, q searchQuery) ([]searchHit, error) {
	root := q.Root
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = home
	}
	return walkTree(ctx, root, q, runtime.NumCPU()*2), nil
}

// walkTree walks root with at most workers ReadDir calls in flight, does not
// follow symlinks, and stops everything the moment the limit is met.
func walkTree(ctx context.Context, root string, q searchQuery, workers int) []searchHit {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if workers < 1 {
		workers = 1
	}
	match := nameMatcher(q.Pattern)
	sem := make(chan struct{}, workers)
	var (
		mu   sync.Mutex
		hits []searchHit
		wg   sync.WaitGroup
	)
	offer := func(h searchHit) {
		mu.Lock()
		defer mu.Unlock()
		if len(hits) < q.Limit {
			hits = append(hits, h)
			if len(hits) == q.Limit {
				cancel()
			}
		}
	}
	var visit func(dir string)
	visit = func(dir string) {
		defer wg.Done()
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		entries, err := os.ReadDir(dir)
		<-sem
		if err != nil {
			return
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return
			}
			path := filepath.Join(dir, e.Name())
			isDir := e.IsDir()
			if isDir && skipDir(path, e.Name()) {
				continue
			}
			if match(e.Name()) && (q.Kind == "" || (q.Kind == "dir") == isDir) {
				offer(searchHit{Path: path, Name: e.Name(), Dir: isDir, Source: "walk"})
			}
			if isDir {
				wg.Add(1)
				go visit(path)
			}
		}
	}
	wg.Add(1)
	visit(root)
	wg.Wait()
	return hits
}
