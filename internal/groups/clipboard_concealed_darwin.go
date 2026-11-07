package groups

import (
	"context"
	"os/exec"
	"time"
)

// clipboardConcealed reports whether the pasteboard carries a marker that the
// copying app (a password manager) asked clipboard managers not to keep, per
// nspasteboard.org. It is only asked when the text changed, so the osascript
// spawn costs nothing in the steady state.
func clipboardConcealed() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e",
		`ObjC.import("AppKit"); ObjC.deepUnwrap($.NSPasteboard.generalPasteboard.types).join("\n")`).Output()
	if err != nil {
		return false
	}
	return concealedTypes(string(out))
}
