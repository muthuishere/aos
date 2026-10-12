package groups

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/windowctl"
)

func TestFindOptionsFromFlags(t *testing.T) {
	opts, set, err := findOptionsFrom([]string{"--app=Calculator", "--text", "7", "--source=ocr", "--role=button", "--exact", "--no-raise", "--all"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Match.App != "Calculator" || opts.Text != "7" || opts.Source != windowctl.FindOCR ||
		opts.Role != "button" || !opts.Exact || !opts.NoRaise || !set.Has("all") {
		t.Fatalf("got %+v", opts)
	}
	// A bare word is the app, as everywhere else.
	opts, _, err = findOptionsFrom([]string{"Safari", "--text=Reload"})
	if err != nil || opts.Match.App != "Safari" {
		t.Fatalf("bare app: %+v, %v", opts, err)
	}
}

func TestFindRejectsMissingInputAndUnknownFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--app=X"},                    // no text
		{"--text=hi"},                  // no window
		{"--app=X", "--text=a", "--x"}, // typo
	} {
		if _, _, err := findOptionsFrom(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

func TestNotFoundMapsToExit3(t *testing.T) {
	err := notFoundExit(fmt.Errorf("find: %w: closest \"Allow\"", windowctl.ErrNoMatch))
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != exitNotFound || exitNotFound != 3 || !strings.Contains(exit.Message, "Allow") {
		t.Fatalf("got %#v", err)
	}
	other := errors.New("permission denied")
	if notFoundExit(other) != other {
		t.Fatal("other errors must pass through unchanged (exit 1)")
	}
}

func TestDrawOptionsFromFlags(t *testing.T) {
	opts, set, err := drawOptionsFrom([]string{
		"--at=10,20", "--text", "click", "--style=ring", "--shape=zigzag", "--size=l",
		"--color=green", "--from=top", "--monitor=2", "--duration=1.5", "--preview", "/tmp/p.png", "--json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Point == nil || *opts.Point != (windowctl.Point{X: 10, Y: 20}) || opts.Text != "click" ||
		opts.Style != windowctl.DrawRing || opts.Shape != windowctl.ShapeZigzag || opts.Size != windowctl.SizeL ||
		opts.Color == nil || opts.From != "top" || opts.Monitor == nil || *opts.Monitor != 2 ||
		opts.Duration != 1500*time.Millisecond || set.String("preview", "") != "/tmp/p.png" {
		t.Fatalf("got %+v", opts)
	}

	opts, _, err = drawOptionsFrom([]string{"--app=Notepad", "--find=File", "--role=menuitem", "--nth=2", "--text=x", "--detach"})
	if err != nil || opts.Match == nil || opts.Match.App != "Notepad" || opts.Find != "File" ||
		opts.FindRole != "menuitem" || opts.FindNth != 2 || opts.Duration != 300*time.Second {
		t.Fatalf("find/detach: %+v, %v", opts, err)
	}
	opts, _, _ = drawOptionsFrom([]string{"--rect=1,2,3,4", "--text=x"})
	if opts.Region == nil || *opts.Region != (windowctl.Rect{X: 1, Y: 2, W: 3, H: 4}) || opts.Duration != 8*time.Second {
		t.Fatalf("rect/default duration: %+v", opts)
	}
}

func TestDrawRejectsBadFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--at=1", "--text=x"},
		{"--rect=1,2,3", "--text=x"},
		{"--at=1,2", "--color=notacolor", "--text=x"},
		{"--at=1,2", "--duration=-1", "--text=x"},
		{"--at=1,2", "--bogus"},
	} {
		if _, _, err := drawOptionsFrom(args); err == nil {
			t.Errorf("%v: want an error", args)
		}
	}
}

// --preview renders through windowctl without showing anything on screen.
func TestDrawPreviewRendersAPNG(t *testing.T) {
	if testing.Short() {
		t.Skip("needs a monitor list")
	}
	if _, err := windowctl.ListMonitors(); err != nil {
		t.Skip("no monitors here:", err)
	}
	out := filepath.Join(t.TempDir(), "d.png")
	var stdout bytes.Buffer
	c := &cli.Ctx{Stdout: &stdout, Stderr: io.Discard, Env: os.Getenv}
	if err := runDraw(c, []string{"--at=100,100", "--text=hi", "--preview=" + out, "--json"}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(out); err != nil || info.Size() == 0 {
		t.Fatalf("no png: %v", err)
	}
	if !strings.Contains(stdout.String(), `"Tip":{"X":100,"Y":100}`) {
		t.Fatalf("json: %s", stdout.String())
	}
}

func registeredCtx(goos string, env map[string]string) (*cli.Ctx, *bytes.Buffer) {
	r := cli.NewRegistry()
	Register(r)
	var out bytes.Buffer
	return &cli.Ctx{
		Registry: r, Stdin: io.NopCloser(strings.NewReader("")), Stdout: &out, Stderr: &out,
		Env: func(k string) string { return env[k] }, GOOS: goos, Version: "test",
	}, &out
}

func TestFindAndDrawNeedADisplay(t *testing.T) {
	c, out := registeredCtx("linux", nil)
	for _, route := range []string{"find", "draw", "draw stop"} {
		var found *cli.Command
		for _, cmd := range c.Registry.Commands() {
			if cmd.Route() == route {
				found = cmd
			}
		}
		if found == nil || !found.NeedsDisplay {
			t.Fatalf("%s: missing or not marked NeedsDisplay", route)
		}
		out.Reset()
		code := cli.Run(c, append(strings.Fields(route), "--app=X", "--text=y"))
		if code != 2 || !strings.Contains(out.String(), "needs a display") {
			t.Errorf("%s on a headless box: exit %d, %q", route, code, out.String())
		}
	}
}

func TestFindAndDrawAreMCPTools(t *testing.T) {
	c, _ := registeredCtx("darwin", nil)
	names := toolNames(buildTools(c, nil, guiAuto))
	for _, want := range []string{"find", "draw", "draw_stop", "draw_config", "draw_fonts"} {
		if !names[want] {
			t.Errorf("MCP tools lack %s", want)
		}
	}
	headless, _ := registeredCtx("linux", nil)
	if names := toolNames(buildTools(headless, nil, guiAuto)); names["find"] || names["draw"] {
		t.Error("find/draw offered on a headless machine")
	}
}

func TestSkillEnsureSkips(t *testing.T) {
	for args, want := range map[string]bool{
		"":                   true,
		"skill install":      true,
		"skill uninstall":    true,
		"install --skills":   true,
		"window list --help": true,
		"help":               true,
		"window list":        false,
		"skill show":         false,
		"--version":          false,
	} {
		if got := skipsSkillEnsure(strings.Fields(args)); got != want {
			t.Errorf("%q: skip = %v, want %v", args, got, want)
		}
	}
}
