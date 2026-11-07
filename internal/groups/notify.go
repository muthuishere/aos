package groups

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("notify", "Post a native desktop notification to the person at the screen")
		r.Add(
			&cli.Command{
				Group: "notify", Name: "",
				Summary: "Post a native notification with a title and an optional body",
				Args:    "<title> [<body>] [--sound]",
				Examples: []string{
					`aos notify "Build finished"`,
					`aos notify "Timer" "Tea is ready" --sound`,
				},
				Run: runNotify,
			},
			&cli.Command{
				Group: "notify", Name: "hold",
				Summary:   "Show the Windows tray balloon for `notify` (started by it)",
				Args:      "<title> <body>",
				Examples:  []string{`aos notify hold "Timer" "Tea is ready"`},
				Hidden:    true,
				Platforms: []string{"windows"},
				Run:       runNotifyHold,
			},
		)
	})
}

func runNotify(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("sound"); err != nil {
		return err
	}
	title, body, err := notifyText(set.Rest)
	if err != nil {
		return err
	}
	return postNotification(title, body, set.Has("sound"))
}

// notifyText takes the title and optional body, trimmed and clipped to what
// every platform shows without cutting mid-character (Windows caps the title
// at 63 and the body at 255 UTF-16 units).
func notifyText(rest []string) (string, string, error) {
	if len(rest) == 0 || strings.TrimSpace(rest[0]) == "" {
		return "", "", fmt.Errorf("`notify` needs a title, e.g. aos notify \"Build finished\"")
	}
	if len(rest) > 2 {
		return "", "", fmt.Errorf("`notify` takes a title and one body; quote text with spaces")
	}
	title := clipRunes(strings.TrimSpace(rest[0]), 63)
	body := ""
	if len(rest) == 2 {
		body = clipRunes(strings.TrimSpace(rest[1]), 255)
	}
	return title, body, nil
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// notifySendArgs is the Linux notify-send line. "--" ends options so a title
// starting with a dash is still text.
func notifySendArgs(title, body string) []string {
	args := []string{"--app-name=aos", "--", title}
	if body != "" {
		args = append(args, body)
	}
	return args
}
