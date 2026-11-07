package groups

import (
	"os"
	"path/filepath"
	"time"
)

func readFocus() (focusState, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return focusState{}, err
	}
	data, err := os.ReadFile(filepath.Join(home, "Library", "DoNotDisturb", "DB", "Assertions.json"))
	if os.IsNotExist(err) {
		return focusState{}, nil // Focus never used on this account
	}
	if err != nil {
		return focusState{}, err
	}
	return parseFocusAssertions(data, time.Now())
}

// macOS has no public API to set a Focus. The supported route is a Shortcut
// the person creates ("Set Focus"), which aos does not assume exists.
func setFocus(on bool, _ time.Duration) error {
	return focusUnsupported("macOS has no public API to turn Focus on or off; use Control Center, or a Shortcut with a \"Set Focus\" action run via `shortcuts run`")
}
