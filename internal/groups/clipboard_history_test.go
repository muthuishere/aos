package groups

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *historyStore {
	t.Helper()
	return newHistoryStore(filepath.Join(t.TempDir(), "clipboard"))
}

func texts(entries []historyEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Text)
	}
	return out
}

func TestHistoryDedupesConsecutive(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	for _, text := range []string{"a", "a", "b", "a", "a"} {
		if _, err := s.Add(text, now); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Load(now)
	if strings.Join(texts(got), ",") != "a,b,a" {
		t.Fatalf("got %v, want a,b,a", texts(got))
	}
}

func TestHistorySkipsEmptyAndHuge(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	for _, text := range []string{"", "  \n", strings.Repeat("x", historyMaxBytes+1)} {
		if ok, _ := s.Add(text, now); ok {
			t.Fatalf("recorded %d bytes that should be skipped", len(text))
		}
	}
	got, _ := s.Load(now)
	if len(got) != 0 {
		t.Fatalf("history should be empty, has %d", len(got))
	}
}

func TestHistoryCap(t *testing.T) {
	s := testStore(t)
	s.MaxEntries = 3
	now := time.Now()
	for i := 1; i <= 5; i++ {
		if _, err := s.Add(strconv.Itoa(i), now); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := s.Load(now)
	if strings.Join(texts(got), ",") != "3,4,5" {
		t.Fatalf("got %v, want the newest 3", texts(got))
	}
	if e, err := newest(got, 1); err != nil || e.Text != "5" {
		t.Fatalf("newest(1) = %q, %v", e.Text, err)
	}
	if _, err := newest(got, 4); err == nil {
		t.Fatal("newest past the end should fail")
	}
}

func TestHistoryExpiry(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	if _, err := s.Add("old", now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add("new", now); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Load(now)
	if strings.Join(texts(got), ",") != "new" {
		t.Fatalf("got %v, want only the entry inside 7 days", texts(got))
	}
}

func TestHistoryClearAndMode(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	if _, err := s.Add("secret-ish", now); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(s.Path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("history file mode %v, want 0600", info.Mode().Perm())
		}
	}
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("clearing an empty history: %v", err)
	}
	got, _ := s.Load(now)
	if len(got) != 0 {
		t.Fatalf("history not cleared: %v", texts(got))
	}
}

func TestConcealedTypes(t *testing.T) {
	if !concealedTypes("public.utf8-plain-text\norg.nspasteboard.ConcealedType\n") {
		t.Fatal("concealed marker not detected")
	}
	if !concealedTypes("text/plain\nx-kde-passwordManagerHint") {
		t.Fatal("KDE hint not detected")
	}
	if concealedTypes("public.utf8-plain-text\nNSStringPboardType") {
		t.Fatal("plain text treated as concealed")
	}
}
