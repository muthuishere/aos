package groups

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/windowctl"
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
			Args:    "[fullscreen|region|window] [--monitor=<n>] [--region=<x,y,w,h>] [--app=<name>] [--copy] [--out=<path>]",
			Aliases: []string{"screenshot"},
			Examples: []string{
				"aos screenshot",
				"aos capture screenshot region --copy",
				"aos capture screenshot --monitor=2",
				"aos capture screenshot --app=Chrome --out=/tmp/term.png",
				"aos capture screenshot --region=0,0,800,600",
			},
			Run: runScreenshot,
		})
	})
}

func runScreenshot(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "out", "monitor", "region", "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("out", "monitor", "region", "app", "title", "copy", "c"); err != nil {
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
	// windowctl can do identically on every OS. Only the interactive modes and
	// clipboard output need the platform's own screenshot tool.
	if targeted(set) {
		if req.Copy {
			return fmt.Errorf("--copy works with the interactive modes; a targeted capture writes a file")
		}
		return targetedScreenshot(c, set, req.Path)
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
// through windowctl, and prints the captured rect so image coordinates can be
// translated back to global click coordinates.
func targetedScreenshot(c *cli.Ctx, set *argSet, outPath string) error {
	opts := windowctl.ScreenshotOptions{OutPath: outPath}

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

	rect, err := windowctl.Screenshot(opts)
	if err != nil {
		return err
	}
	if opts.Match != nil {
		noteVideo(c, "action", "capture screenshot", &rect, describeMatch(*opts.Match))
	}
	c.Printf("%s  %dx%d%s%s\n", outPath, rect.W, rect.H, signed(rect.X), signed(rect.Y))
	return nil
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
