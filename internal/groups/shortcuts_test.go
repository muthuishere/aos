package groups

import (
	"strings"
	"testing"
)

func TestParseShortcutsList(t *testing.T) {
	got := parseShortcutsList("Resize Image (1B2C3D4E-0000-1111-2222-333344445555)\nTimer (v2) (AABBCCDD-0000-1111-2222-333344445555)\nPlain Name\n\n")
	want := []shortcutInfo{
		{Name: "Resize Image", ID: "1B2C3D4E-0000-1111-2222-333344445555"},
		{Name: "Timer (v2)", ID: "AABBCCDD-0000-1111-2222-333344445555"},
		{Name: "Plain Name"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestShortcutsRunArgs(t *testing.T) {
	got := strings.Join(shortcutsRunArgs("Resize Image", "/tmp/in.jpg", "/tmp/out"), "|")
	if got != "run|Resize Image|--input-path|/tmp/in.jpg|--output-path|/tmp/out|--output-type|public.plain-text" {
		t.Errorf("got %q", got)
	}
	if strings.Contains(strings.Join(shortcutsRunArgs("X", "", "/o"), " "), "--input-path") {
		t.Error("no input should mean no --input-path")
	}
}
