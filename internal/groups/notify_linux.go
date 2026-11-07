package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// postNotification uses notify-send (libnotify), else the freedesktop
// notification service over D-Bus directly. The sound flag has no portable
// meaning there; the desktop's own notification sound applies.
func postNotification(title, body string, _ bool) error {
	if sys.Has("notify-send") {
		_, err := sys.Output("notify-send", notifySendArgs(title, body)...)
		return err
	}
	if sys.Has("gdbus") {
		_, err := sys.Output("gdbus", "call", "--session",
			"--dest=org.freedesktop.Notifications", "--object-path=/org/freedesktop/Notifications",
			"--method=org.freedesktop.Notifications.Notify",
			"aos", "0", "", title, body, "[]", "{}", "-1")
		return err
	}
	return &cli.ExitError{Code: 2, Message: "`notify` needs notify-send (libnotify) or gdbus; install libnotify-bin"}
}

func runNotifyHold(*cli.Ctx, []string) error { return errUnsupported }
