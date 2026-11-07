package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

func postNotification(title, body string, sound bool) error {
	_, err := sys.Osascript(notifyScript(title, body, sound))
	return err
}

func runNotifyHold(*cli.Ctx, []string) error { return errUnsupported }

// notifyScript is the AppleScript that posts the notification on macOS.
func notifyScript(title, body string, sound bool) string {
	script := "display notification " + appleScriptString(body) + " with title " + appleScriptString(title)
	if sound {
		script += ` sound name "default"`
	}
	return script
}
