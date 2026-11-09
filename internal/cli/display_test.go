package cli

import (
	"bytes"
	"strings"
	"testing"
)

func envOf(pairs map[string]string) func(string) string {
	return func(key string) string { return pairs[key] }
}

func TestHasDisplay(t *testing.T) {
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want bool
	}{
		{"linux with X11", "linux", map[string]string{"DISPLAY": ":0"}, true},
		{"linux with wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-1"}, true},
		{"linux headless", "linux", nil, false},
		{"linux blank display", "linux", map[string]string{"DISPLAY": "  "}, false},
		{"macos assumes a session", "darwin", nil, true},
		{"windows assumes a session", "windows", nil, true},
	}
	for _, tc := range cases {
		if got := HasDisplay(envOf(tc.env), tc.goos); got != tc.want {
			t.Errorf("%s: HasDisplay = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestGUICommandRefusedWithoutDisplay(t *testing.T) {
	r := NewRegistry()
	r.Describe("window", "Windows")
	r.Add(&Command{Group: "window", Name: "list", Summary: "List", NeedsDisplay: true,
		Run: func(c *Ctx, _ []string) error {
			t.Fatal("the runner must not be reached on a headless machine")
			return nil
		}})

	var out, errOut bytes.Buffer
	c := &Ctx{Registry: r, Stdout: &out, Stderr: &errOut, Env: envOf(nil), GOOS: "linux"}
	if code := Run(c, []string{"window", "list"}); code != 2 {
		t.Fatalf("want exit 2, got %d", code)
	}
	if !strings.Contains(errOut.String(), "needs a display") {
		t.Fatalf("want an explanation, got %q", errOut.String())
	}
}

func TestGUICommandRunsWithDisplay(t *testing.T) {
	r := NewRegistry()
	r.Describe("window", "Windows")
	ran := false
	r.Add(&Command{Group: "window", Name: "list", Summary: "List", NeedsDisplay: true,
		Run: func(c *Ctx, _ []string) error { ran = true; return nil }})

	var out bytes.Buffer
	c := &Ctx{Registry: r, Stdout: &out, Stderr: &out,
		Env: envOf(map[string]string{"DISPLAY": ":99"}), GOOS: "linux"}
	if code := Run(c, []string{"window", "list"}); code != 0 || !ran {
		t.Fatalf("exit=%d ran=%v", code, ran)
	}
}

func TestCommandsListHidesGUIWhenHeadless(t *testing.T) {
	r := NewRegistry()
	r.Describe("window", "Windows")
	r.Describe("file", "Files")
	noop := func(*Ctx, []string) error { return nil }
	r.Add(
		&Command{Group: "window", Name: "list", Summary: "List windows", NeedsDisplay: true, Run: noop},
		&Command{Group: "file", Name: "read", Summary: "Read a file", Run: noop},
	)

	var out bytes.Buffer
	c := &Ctx{Registry: r, Stdout: &out, Stderr: &out, Env: envOf(nil), GOOS: "linux"}
	if code := Run(c, []string{"commands"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out.String(), "window list") {
		t.Fatalf("GUI command should be hidden when headless:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "file read") {
		t.Fatalf("screenless command should still list:\n%s", out.String())
	}

	out.Reset()
	if code := Run(c, []string{"commands", "--all"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "g^ window list") {
		t.Fatalf("--all should mark the GUI command with g:\n%s", out.String())
	}
}
