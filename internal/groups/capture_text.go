package groups

import (
	"fmt"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "capture", Name: "text",
			Summary: "OCR a screen region or a whole monitor and print its text in reading order",
			Args:    "[--region=<x,y,w,h>] [--monitor=<n>] [--max-chars=<n>] [--json]",
			Examples: []string{
				"aos capture text --region=100,100,800,400",
				"aos capture text --monitor=2 --json",
				"aos capture text --monitor=1 --region=0,0,600,300   # region relative to monitor 1",
				"# a window's text: aos window text --app=Preview --source=ocr",
			},
			Run: runCaptureText,
		})
	})
}

func runCaptureText(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "region", "monitor", "max-chars")
	if err != nil {
		return err
	}
	if err := set.Reject("region", "monitor", "max-chars", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`capture text` takes no positional arguments; for a window use `window text --source=ocr`")
	}
	opts := engine.ScreenTextOptions{}
	if opts.Monitor, err = set.IntPtr("monitor"); err != nil {
		return err
	}
	if opts.MaxChars, err = set.Int("max-chars", 0); err != nil {
		return err
	}
	if region := set.String("region", ""); region != "" {
		rect, err := parseRect(region)
		if err != nil {
			return err
		}
		opts.Region = &rect
	}
	t, rect, err := engine.ReadScreenText(opts)
	if err != nil {
		return err
	}
	if set.Has("json") {
		return writeJSON(c, struct {
			Rect engine.Rect `json:"rect"`
			engine.WindowText
		}{rect, t})
	}
	if t.Text != "" {
		c.Println(t.Text)
	}
	return nil
}
