package groups

import "fmt"

// showSubtitle is the fallback subtitle.go reaches for when the engine overlay
// could not be spawned. Windows has no second backend: aos never shells out to
// PowerShell (antivirus flags a hidden PowerShell started by aos.exe), so the
// native Win32 overlay is the only way a caption is drawn. Say so plainly
// rather than pretending something else is on screen.
func showSubtitle(req subtitleRequest) (string, error) {
	return "", fmt.Errorf("the Windows caption overlay could not start, and there is no fallback; check `aos draw text %q` works on this desktop", req.Text)
}
