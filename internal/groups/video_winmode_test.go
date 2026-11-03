package groups

import (
	"strings"
	"testing"
)

func TestParseWindowsMode(t *testing.T) {
	o, err := parseVideoStart([]string{"windows", "--app=Ghostty, Google Chrome", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Mode != "windows" || !o.DryRun || len(o.Apps) != 2 || o.Apps[0] != "Ghostty" || o.Apps[1] != "Google Chrome" {
		t.Errorf("got %+v", o)
	}
	for _, bad := range [][]string{{"windows"}, {"window", "--app=X", "--dry-run"}} {
		if _, err := parseVideoStart(bad); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}

func TestJoinArgs(t *testing.T) {
	one := strings.Join(joinArgs([]string{"a.mp4"}, "o.mp4", 30, 1080), " ")
	if !strings.Contains(one, "[0:v]fps=30,scale=-2:1080,setsar=1[v0];[v0]format=yuv420p[v]") || strings.Contains(one, "hstack") {
		t.Errorf("one: %s", one)
	}
	two := strings.Join(joinArgs([]string{"a.mp4", "b.mp4"}, "o.mp4", 25, 1080), " ")
	for _, want := range []string{"-i a.mp4 -i b.mp4", "[1:v]fps=25,scale=-2:1080", "[v0][v1]hstack=inputs=2:shortest=0", "-map [v]", "o.mp4"} {
		if !strings.Contains(two, want) {
			t.Errorf("missing %q in %s", want, two)
		}
	}
}

func TestWinSlug(t *testing.T) {
	for in, want := range map[string]string{"Google Chrome": "google-chrome", "‎WhatsApp": "whatsapp", "": "window"} {
		if got := winSlug(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
