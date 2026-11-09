package groups

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeSized(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// topTree: big/ holds 256 KB (deep), small/ 16 KB, a node_modules 64 KB, and
// a loose 128 KB file at the top.
func topTree(t *testing.T) string {
	root := t.TempDir()
	writeSized(t, filepath.Join(root, "big", "a", "b", "deep.bin"), 256<<10)
	writeSized(t, filepath.Join(root, "small", "s.txt"), 16<<10)
	writeSized(t, filepath.Join(root, "app", "node_modules", "dep.js"), 64<<10)
	writeSized(t, filepath.Join(root, "loose.iso"), 128<<10)
	return root
}

func TestDiskTopRanksFolders(t *testing.T) {
	root := topTree(t)
	rep := diskTop(context.Background(), root, topOptions{Depth: 1, Limit: 10, Workers: 4}, nil)
	if rep.Partial {
		t.Fatal("a finished walk must not be partial")
	}
	var names []string
	for _, e := range rep.Entries {
		if !e.Dir {
			t.Errorf("file %s listed without --files", e.Path)
		}
		names = append(names, e.Name)
	}
	if len(names) != 3 || names[0] != "big" || names[1] != "app" || names[2] != "small" {
		t.Fatalf("order = %v, want [big app small]", names)
	}
	if rep.Entries[0].Bytes < 256<<10 {
		t.Errorf("big = %d bytes, want the deep file counted (>= 256 KB)", rep.Entries[0].Bytes)
	}
	if rep.TotalBytes < (256+16+64+128)<<10 {
		t.Errorf("total = %d, want every file counted", rep.TotalBytes)
	}
}

func TestDiskTopFilesDepthAndLimit(t *testing.T) {
	root := topTree(t)
	rep := diskTop(context.Background(), root, topOptions{Depth: 2, Limit: 2, Files: true, Workers: 2}, nil)
	if len(rep.Entries) != 2 {
		t.Fatalf("limit 2 gave %d rows", len(rep.Entries))
	}
	if rep.Entries[0].Name != "big" || rep.Entries[1].Name != "a" {
		t.Errorf("top two = %s, %s; want big then big/a", rep.Entries[0].Name, rep.Entries[1].Name)
	}
	all := diskTop(context.Background(), root, topOptions{Depth: 2, Limit: 100, Files: true, Workers: 2}, nil)
	found := map[string]topEntry{}
	for _, e := range all.Entries {
		found[e.Name] = e
	}
	if e, ok := found["loose.iso"]; !ok || e.Dir || e.Depth != 1 {
		t.Errorf("loose.iso row = %+v, want a depth-1 file", e)
	}
	if _, ok := found["deep.bin"]; ok {
		t.Error("deep.bin is at depth 4 and must not be a row at --depth=2")
	}
	if e := found["node_modules"]; e.Tag != "node_modules" || e.Safe != "yes" {
		t.Errorf("node_modules tag = %+v", e)
	}
}

func TestDiskTopTimeoutIsPartial(t *testing.T) {
	root := topTree(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if rep := diskTop(ctx, root, topOptions{Depth: 1, Limit: 10, Workers: 1}, nil); !rep.Partial {
		t.Error("a walk past its deadline must report partial")
	}
}

func TestDiskTopTagsKnownPlaces(t *testing.T) {
	root := topTree(t)
	places := map[string]place{placeKey(filepath.Join(root, "small")): {"cache", "test", "yes"}}
	rep := diskTop(context.Background(), root, topOptions{Depth: 1, Limit: 10, Workers: 2}, places)
	for _, e := range rep.Entries {
		if e.Name == "small" && e.Tag != "cache" {
			t.Errorf("small tag = %q, want cache", e.Tag)
		}
		if e.Name == "big" && e.Tag != "" {
			t.Errorf("big tagged %q", e.Tag)
		}
	}
}

func TestKnownPlacesCoversHomeFolders(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home")
	}
	m := knownPlaces()
	for path, tag := range map[string]string{
		filepath.Join(home, "Downloads"):                                              "downloads",
		filepath.Join(home, "Library", "Caches"):                                      "cache",
		filepath.Join(home, ".cache"):                                                 "cache",
		filepath.Join(home, "Library", "Developer", "Xcode", "DerivedData"):           "xcode-derived-data",
		filepath.Join(home, "Library", "Application Support", "MobileSync", "Backup"): "ios-backup",
	} {
		if got := m[placeKey(path)].Tag; got != tag {
			t.Errorf("%s tag = %q, want %q", path, got, tag)
		}
	}
}
