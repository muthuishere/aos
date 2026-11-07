package groups

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNameMatcher(t *testing.T) {
	cases := []struct {
		pattern, name string
		want          bool
	}{
		{"invoice", "Invoice-2026.PDF", true},
		{"INV", "my_invoice.txt", true},
		{"invoice", "receipt.txt", false},
		{"*.pdf", "Report.PDF", true},
		{"*.pdf", "report.pdf.txt", false},
		{"rep?rt*", "Report-final.doc", true},
	}
	for _, c := range cases {
		if got := nameMatcher(c.pattern)(c.name); got != c.want {
			t.Errorf("nameMatcher(%q)(%q) = %v, want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestLiteralCore(t *testing.T) {
	for pattern, want := range map[string]string{"*.pdf": ".pdf", "invoice": "invoice", "a*report*b": "report"} {
		if got := literalCore(pattern); got != want {
			t.Errorf("literalCore(%q) = %q, want %q", pattern, got, want)
		}
	}
}

func TestSkipDir(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		path string
		want bool
	}{
		{"a" + sep + "node_modules", true},
		{"a" + sep + ".git", true},
		{"C:" + sep + "$Recycle.Bin", true},
		{"C:" + sep + "Windows" + sep + "WinSxS", true},
		{"x" + sep + "WinSxS", false},
		{"a" + sep + "src", false},
	}
	for _, c := range cases {
		if got := skipDir(c.path, filepath.Base(c.path)); got != c.want {
			t.Errorf("skipDir(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func makeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"notes.txt", "a/notes-2.md", "a/b/c/NOTES.pdf", "notes-dir/x.txt",
		"node_modules/notes.js", ".git/notes", "other.txt",
	} {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestWalkTreeMatchSkipAndKind(t *testing.T) {
	root := makeTree(t)
	hits := walkTree(context.Background(), root, searchQuery{Pattern: "notes", Limit: 50}, 4)
	got := map[string]bool{}
	for _, h := range hits {
		rel, _ := filepath.Rel(root, h.Path)
		got[filepath.ToSlash(rel)] = true
		if h.Source != "walk" {
			t.Errorf("source = %q, want walk", h.Source)
		}
	}
	for _, want := range []string{"notes.txt", "a/notes-2.md", "a/b/c/NOTES.pdf", "notes-dir"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("want 4 hits (node_modules and .git skipped), got %v", got)
	}

	dirs := walkTree(context.Background(), root, searchQuery{Pattern: "notes", Kind: "dir", Limit: 50}, 4)
	if len(dirs) != 1 || !dirs[0].Dir {
		t.Errorf("--kind=dir: got %+v", dirs)
	}
	files := walkTree(context.Background(), root, searchQuery{Pattern: "*.pdf", Kind: "file", Limit: 50}, 4)
	if len(files) != 1 || files[0].Name != "NOTES.pdf" {
		t.Errorf("glob --kind=file: got %+v", files)
	}
}

func TestWalkTreeLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 30; i++ {
		dir := filepath.Join(root, "d", string(rune('a'+i%26)), "x")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "hit"+string(rune('a'+i))+".txt"), nil, 0o644)
	}
	hits := walkTree(context.Background(), root, searchQuery{Pattern: "hit", Limit: 3}, 8)
	if len(hits) != 3 {
		t.Fatalf("want exactly 3 hits, got %d", len(hits))
	}
}

func TestCollectorFiltersIndexOutput(t *testing.T) {
	root := makeTree(t)
	col := newCollector(searchQuery{Pattern: "notes", Root: filepath.Join(root, "a"), Limit: 1}, "mdfind")
	more := col.add(filepath.Join(root, "notes.txt")) // outside root
	if !more || len(col.hits) != 0 {
		t.Fatalf("outside-root path kept: %+v", col.hits)
	}
	col.add(filepath.Join(root, "a", "gone-notes.txt")) // stale entry
	if more := col.add(filepath.Join(root, "a", "notes-2.md")); more {
		t.Error("add should report the limit was reached")
	}
	if len(col.hits) != 1 || col.hits[0].Source != "mdfind" {
		t.Errorf("hits = %+v", col.hits)
	}
}

func fakeEngine(name string, hits []searchHit, err error) searchEngine {
	return searchEngine{Name: name, Run: func(context.Context, searchQuery) ([]searchHit, error) { return hits, err }}
}

func TestSelectEngines(t *testing.T) {
	platform := []searchEngine{fakeEngine("everything", nil, nil), fakeEngine("winsearch", nil, nil)}
	names := func(es []searchEngine) (out []string) {
		for _, e := range es {
			out = append(out, e.Name)
		}
		return
	}
	cases := map[string][]string{
		"auto":       {"everything", "winsearch", "walk"},
		"walk":       {"walk"},
		"index":      {"winsearch"},
		"everything": {"everything"},
	}
	for choice, want := range cases {
		got, err := selectEngines(choice, platform)
		if err != nil || len(got) != len(want) {
			t.Fatalf("%s: got %v, %v", choice, names(got), err)
		}
		for i := range want {
			if got[i].Name != want[i] {
				t.Errorf("%s: got %v, want %v", choice, names(got), want)
			}
		}
	}
	if _, err := selectEngines("bogus", platform); err == nil {
		t.Error("bogus engine accepted")
	}
	if _, err := selectEngines("everything", nil); err == nil {
		t.Error("everything accepted with no Everything engine")
	}
}

func TestRunSearchFallsThrough(t *testing.T) {
	want := []searchHit{{Path: "/x", Source: "walk"}}
	engines := []searchEngine{
		fakeEngine("missing", nil, errEngineUnavailable),
		fakeEngine("empty", nil, nil),
		fakeEngine("walk", want, nil),
	}
	got, err := runSearch(context.Background(), engines, searchQuery{Limit: 5})
	if err != nil || len(got) != 1 || got[0].Source != "walk" {
		t.Fatalf("got %+v, %v", got, err)
	}

	first := []searchHit{{Path: "/i", Source: "mdfind"}}
	got, _ = runSearch(context.Background(), []searchEngine{fakeEngine("mdfind", first, nil), fakeEngine("walk", want, nil)}, searchQuery{Limit: 5})
	if got[0].Source != "mdfind" {
		t.Errorf("index answer not used: %+v", got)
	}

	_, err = runSearch(context.Background(), []searchEngine{fakeEngine("only", nil, errEngineUnavailable)}, searchQuery{Limit: 5})
	if !errors.Is(err, errEngineUnavailable) {
		t.Errorf("want unavailable error, got %v", err)
	}
}
