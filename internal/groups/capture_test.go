package groups

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/engine"
)

// These tests cover the argument handling of `capture screenshot` only. Every
// case here fails before any capture is attempted, so nothing on this machine's
// screen — or absence of one — changes the result.

// A named target (monitor, region, window) is a deterministic capture windowctl
// does identically on every OS; only the interactive modes go to the platform's
// own screenshot tool. Getting that split wrong silently changes which backend
// runs, so it is asserted directly.
func TestTargetedRecognisesEveryNamedTarget(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"--monitor=2"}, true},
		{[]string{"--region=0,0,800,600"}, true},
		{[]string{"--app=Chrome"}, true},
		{[]string{"--title=Inbox"}, true},
		{[]string{"region"}, false}, // the interactive mode, not a target
		{[]string{"--copy"}, false}, // output choice, not a target
		{nil, false},                // plain fullscreen
	}
	for _, tc := range cases {
		set, err := parseArgs(tc.args, "out", "monitor", "region", "app", "title")
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if got := targeted(set); got != tc.want {
			t.Errorf("targeted(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// A targeted capture writes a file; there is no interactive step to put on the
// clipboard. Saying so is better than producing an empty clipboard.
func TestScreenshotRefusesCopyWithATargetedCapture(t *testing.T) {
	for _, args := range [][]string{
		{"--copy", "--monitor=1"},
		{"--copy", "--region=0,0,10,10"},
		{"--copy", "--app=Chrome"},
	} {
		c, _, _ := testCtx("")
		err := runScreenshot(c, args)
		if err == nil {
			t.Fatalf("%v was allowed", args)
		}
		if !strings.Contains(err.Error(), "--copy") {
			t.Fatalf("%v failed for the wrong reason: %v", args, err)
		}
	}
}

func TestScreenshotRejectsUnknownModesAndFlags(t *testing.T) {
	cases := map[string][]string{
		"unknown positional": {"--copy", "everything"},
		"typo'd flag":        {"--monitr=1"},
		"bad monitor value":  {"--monitor=two", "--out=" + filepath.Join(t.TempDir(), "x.png")},
		"bad region shape":   {"--region=0,0,800", "--out=" + filepath.Join(t.TempDir(), "x.png")},
	}
	for name, args := range cases {
		c, _, _ := testCtx("")
		if err := runScreenshot(c, args); err == nil {
			t.Errorf("%s: `capture screenshot %v` must fail", name, args)
		}
	}
}

// The three spellings of each mode exist because an agent will type any of
// them; they must all reach the same capture.
func TestScreenshotModeAliasesAreAccepted(t *testing.T) {
	for _, arg := range []string{"fullscreen", "full", "screen", "region", "area", "select", "window", "win"} {
		set, err := parseArgs([]string{arg, "--copy"}, "out", "monitor", "region", "app", "title")
		if err != nil {
			t.Fatalf("%q: %v", arg, err)
		}
		if len(set.Rest) != 1 || set.Rest[0] != arg {
			t.Fatalf("%q was not kept as a positional: %v", arg, set.Rest)
		}
	}
}

// --cover flags are checked before anything is captured.
func TestScreenshotCoverFlagsFailBeforeCapture(t *testing.T) {
	out := filepath.Join(t.TempDir(), "x.png")
	cases := map[string]struct {
		args []string
		want string
	}{
		"cover with copy":        {[]string{"--copy", "--cover=0,0,10,10"}, "painted into a file"},
		"cover in region mode":   {[]string{"region", "--cover=0,0,10,10", "--out=" + out}, "painted into a file"},
		"bad cover rect":         {[]string{"--cover=0,0,10", "--out=" + out}, "--cover"},
		"empty cover":            {[]string{"--cover=;", "--out=" + out}, "--cover needs"},
		"style without a cover":  {[]string{"--cover-color=blue", "--out=" + out}, "needs a cover"},
		"unknown preset":         {[]string{"--cover=0,0,1,1", "--cover-preset=nope", "--out=" + out}, "no preset"},
		"bad pad":                {[]string{"--cover-find=x", "--cover-pad=wide", "--out=" + out}, "--cover-pad"},
		"pad without a cover":    {[]string{"--cover-pad=3", "--out=" + out}, "needs a cover"},
		"text colour, no target": {[]string{"--cover-text-color=white", "--out=" + out}, "needs a cover"},
	}
	for name, tc := range cases {
		c, _, _ := testCtx("")
		err := runScreenshot(c, tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to mention %q", name, err, tc.want)
		}
	}
}

func TestScreenshotCoversFromFlags(t *testing.T) {
	set, err := parseArgs([]string{"--cover=1,2,3,4; 5,6,7,8", "--cover-find=ghp_", "--cover-preset=private", "--cover-color=#1e40af", "--cover-pad=2"},
		screenshotCoverFlags...)
	if err != nil {
		t.Fatal(err)
	}
	covers, err := screenshotCovers(set)
	if err != nil {
		t.Fatal(err)
	}
	var mine int
	for _, c := range covers {
		if c.Style.Text == "Private" {
			mine++
			if c.Style.Color != "#1e40af" || c.Pad != 2 {
				t.Errorf("cover %+v: flags must override the preset", c)
			}
		}
	}
	if mine != 3 || *covers[0].Rect != (engine.Rect{X: 1, Y: 2, W: 3, H: 4}) || covers[2].Find != "ghp_" || covers[2].Match != nil {
		t.Errorf("covers = %+v", covers)
	}
}
