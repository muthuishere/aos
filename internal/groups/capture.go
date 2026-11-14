package groups

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// captureMode is what part of the screen a screenshot covers.
type captureMode string

const (
	captureFullscreen captureMode = "fullscreen"
	captureRegion     captureMode = "region"
	captureWindow     captureMode = "window"
)

// captureRequest is the platform-independent description of a screenshot.
type captureRequest struct {
	Mode captureMode
	Path string // where to write; ignored when Clipboard is set
	Copy bool   // put the image on the clipboard instead of on disk
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("capture", "Screenshots")
		r.Add(&cli.Command{
			Group: "capture", Name: "screenshot",
			Summary: "Take a screenshot",
			Args: "[fullscreen|region|window] [--monitor=<n>] [--region=<x,y,w,h>] [--app=<name>] [--copy] [--out=<path>] " +
				"[--cover=<x,y,w,h[;x,y,w,h...]>] [--cover-app=<name> | --cover-title=<s>] [--cover-find=<text>] [--cover-pad=<px>] " +
				"[--cover-color=<c>] [--cover-text=<s>] [--cover-text-color=<c>] [--cover-preset=<name>]",
			Aliases: []string{"screenshot"},
			Examples: []string{
				"aos screenshot",
				"aos capture screenshot region --copy",
				"aos capture screenshot --monitor=2",
				"aos capture screenshot --app=Chrome --out=/tmp/term.png",
				"aos capture screenshot --region=0,0,800,600",
				`aos screenshot --cover-app=Preview --out=/tmp/s.png`,
				`aos screenshot --app=Terminal --cover-app=Terminal --cover-find=ghp_ --out=/tmp/t.png`,
				`aos screenshot --cover=10,10,300,40 --cover-text="Private" --out=/tmp/s.png`,
				"# --cover* paints the covers into the file's pixels; live `draw cover` covers are painted in too",
				"# fail closed: a cover that cannot be placed (exit 3), a tint, or no permission writes no file",
			},
			Run: runScreenshot,
		})
	})
}

func runScreenshot(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, append([]string{"out", "monitor", "region", "app", "title"}, screenshotCoverFlags...)...)
	if err != nil {
		return err
	}
	if err := set.Reject(append([]string{"out", "monitor", "region", "app", "title", "copy", "c"}, screenshotCoverFlags...)...); err != nil {
		return err
	}
	covers, err := screenshotCovers(set)
	if err != nil {
		return err
	}

	req := captureRequest{Mode: captureFullscreen, Path: set.String("out", "")}
	req.Copy = set.Has("copy") || set.Has("c")
	for _, arg := range set.Rest {
		switch arg {
		case "fullscreen", "full", "screen":
			req.Mode = captureFullscreen
		case "region", "area", "select":
			req.Mode = captureRegion
		case "window", "win":
			req.Mode = captureWindow
		default:
			return fmt.Errorf("unknown argument %q for `capture screenshot`", arg)
		}
	}

	if req.Path == "" && !req.Copy {
		path, err := defaultScreenshotPath()
		if err != nil {
			return err
		}
		req.Path = path
	}
	if req.Path != "" {
		if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
			return err
		}
	}

	// A named target (monitor, region, or window) is a deterministic capture
	// the engine can do identically on every OS. Only the interactive modes and
	// clipboard output need the platform's own screenshot tool.
	if len(covers) > 0 {
		// Redaction happens in the engine's pixels: no interactive mode,
		// no clipboard, and the default full screen is the focused monitor.
		if req.Copy || req.Mode != captureFullscreen {
			return fmt.Errorf("covers are painted into a file: use --monitor, --region, --app or the default full screen, with --out")
		}
		return targetedScreenshot(c, set, req.Path, covers)
	}
	if targeted(set) {
		if req.Copy {
			return fmt.Errorf("--copy works with the interactive modes; a targeted capture writes a file")
		}
		return targetedScreenshot(c, set, req.Path, nil)
	}

	if err := takeScreenshot(req); err != nil {
		return err
	}
	if req.Copy {
		c.Println("copied to clipboard")
		return nil
	}
	// A cancelled interactive capture leaves no file; say so rather than
	// pointing at a path that does not exist.
	if _, err := os.Stat(req.Path); err != nil {
		return &cli.ExitError{Code: 1, Message: "capture cancelled"}
	}
	c.Println(req.Path)
	return nil
}

func targeted(set *argSet) bool {
	return set.Has("monitor") || set.Has("region") || set.Has("app") || set.Has("title")
}

// targetedScreenshot captures a monitor, an explicit rect, or a named window
// through the engine, and prints the captured rect so image coordinates can be
// translated back to global click coordinates.
func targetedScreenshot(c *cli.Ctx, set *argSet, outPath string, covers []engine.ScreenCover) error {
	opts := engine.ScreenshotOptions{OutPath: outPath, Covers: covers}

	monitor, err := set.IntPtr("monitor")
	if err != nil {
		return err
	}
	opts.Monitor = monitor

	if region := set.String("region", ""); region != "" {
		rect, err := parseRect(region)
		if err != nil {
			return err
		}
		opts.Region = &rect
	}
	if match, ok := optionalMatch(set); ok {
		opts.Match = &match
	}

	res, err := engine.ScreenshotCovered(opts)
	if err != nil {
		if errors.Is(err, engine.ErrNoMatch) {
			return &cli.ExitError{Code: exitNotFound, Message: err.Error() + " (no file written)"}
		}
		if len(covers) > 0 {
			return &cli.ExitError{Code: 1, Message: err.Error() + " (no file written)"}
		}
		return err
	}
	rect := res.Rect
	if opts.Match != nil {
		noteVideo(c, "action", "capture screenshot", &rect, describeMatch(*opts.Match))
	}
	note := ""
	if len(covers) > 0 {
		note = fmt.Sprintf("  (%d area(s) covered)", len(res.Covered))
	}
	c.Printf("%s  %dx%d%s%s%s\n", outPath, rect.W, rect.H, signed(rect.X), signed(rect.Y), note)
	return nil
}

// screenshotCoverFlags are the --cover* flags of `capture screenshot`.
var screenshotCoverFlags = []string{
	"cover", "cover-app", "cover-title", "cover-find", "cover-pad",
	"cover-color", "cover-text", "cover-text-color", "cover-preset",
}

// screenshotCovers turns the --cover* flags, plus every live opaque
// `draw cover`, into the areas to paint into the file.
func screenshotCovers(set *argSet) ([]engine.ScreenCover, error) {
	style := engine.CoverStyle{Color: set.String("cover-color", ""), Text: set.String("cover-text", ""),
		TextColor: set.String("cover-text-color", "")}
	if name := set.String("cover-preset", ""); name != "" {
		p, err := engine.CoverPreset(name)
		if err != nil {
			return nil, err
		}
		style = p.Merge(style)
	}
	pad, err := set.Int("cover-pad", 0)
	if err != nil {
		return nil, err
	}
	var covers []engine.ScreenCover
	for _, part := range strings.Split(set.String("cover", ""), ";") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		r, err := parseRect(part)
		if err != nil {
			return nil, fmt.Errorf("--cover: %w", err)
		}
		covers = append(covers, engine.ScreenCover{Rect: &r, Pad: pad, Style: style})
	}
	match := engine.Match{App: set.String("cover-app", ""), Title: set.String("cover-title", "")}
	find := set.String("cover-find", "")
	switch {
	case match.App != "" || match.Title != "":
		if resolved, err := resolveMatch(match); err == nil {
			match = resolved
		}
		covers = append(covers, engine.ScreenCover{Match: &match, Find: find, Pad: pad, Style: style})
	case find != "":
		covers = append(covers, engine.ScreenCover{Find: find, Pad: pad, Style: style})
	}
	if set.Has("cover") && len(covers) == 0 {
		return nil, fmt.Errorf("--cover needs x,y,w,h")
	}
	if len(covers) == 0 {
		for _, f := range []string{"cover-color", "cover-text", "cover-text-color", "cover-preset", "cover-pad"} {
			if set.Has(f) {
				return nil, fmt.Errorf("--%s needs a cover: --cover, --cover-app/--cover-title or --cover-find", f)
			}
		}
	}
	for _, lc := range engine.LiveCovers() {
		if lc.Cover.Hidden || lc.Cover.Tint {
			continue
		}
		r := lc.Cover.Rect
		covers = append(covers, engine.ScreenCover{Rect: &r, Style: lc.Cover.Style})
	}
	return covers, nil
}

// defaultScreenshotPath is a timestamped PNG in the user's pictures folder.
func defaultScreenshotPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Pictures", "Screenshots")
	name := "screenshot-" + time.Now().Format("2006-01-02-150405") + ".png"
	return filepath.Join(dir, name), nil
}
