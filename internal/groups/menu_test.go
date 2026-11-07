package groups

import "testing"

func TestParseMenuDump(t *testing.T) {
	got := parseMenuDump("1\tFile\tExport as PDF…\n0\tEdit\tUndo\n1\tFile\tOpen Recent\tnotes.txt\nbad\n")
	if len(got) != 3 || got[0] != (menuItem{Path: "File > Export as PDF…", Enabled: true}) || got[1].Enabled || got[2].Path != "File > Open Recent > notes.txt" {
		t.Errorf("got %+v", got)
	}
}

func TestMatchMenu(t *testing.T) {
	items := []menuItem{
		{Path: "File > Export", Enabled: true},
		{Path: "File > Export as PDF…", Enabled: true},
		{Path: "Edit > Copy", Enabled: true},
		{Path: "Edit > Copy Style", Enabled: true},
	}
	for q, want := range map[string]string{
		"File > Export":          "File > Export",         // exact beats substring
		"file>export as pdf...":  "File > Export as PDF…", // normalised exact
		"as pdf":                 "File > Export as PDF…", // unique substring
		"  edit  >  copy style ": "Edit > Copy Style",
		"copy s":                 "Edit > Copy Style",
	} {
		got, err := matchMenu(items, q)
		if err != nil || got.Path != want {
			t.Errorf("matchMenu(%q) = %q, %v; want %q", q, got.Path, err, want)
		}
	}
	for _, q := range []string{"export", "copy", "nope", "  "} {
		if _, err := matchMenu(items, q); err == nil {
			t.Errorf("matchMenu(%q) should be refused", q)
		}
	}
}
