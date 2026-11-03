package groups

import (
	"fmt"
	"os/exec"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func clipboardWrite(text string) error {
	// clip.exe takes the text on stdin verbatim, which avoids quoting and
	// newline mangling.
	cmd := exec.Command("clip.exe")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// clipboardRead reads CF_UNICODETEXT straight from the Win32 clipboard.
func clipboardRead() (string, error) {
	if err := openClipboard(); err != nil {
		return "", err
	}
	defer procCloseClipboard.Call()
	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return "", nil // no text on the clipboard
	}
	p, _, err := procGlobalLock.Call(h)
	if p == 0 {
		return "", fmt.Errorf("GlobalLock: %v", err)
	}
	defer procGlobalUnlock.Call(h)
	// Bound the read by the block's size, not just the NUL, so a malformed
	// buffer cannot run us off the end.
	size, _, _ := procGlobalSize.Call(h)
	n := int(size / 2)
	u := unsafe.Slice((*uint16)(osPtr(p)), n)
	for i, c := range u {
		if c == 0 {
			u = u[:i]
			break
		}
	}
	return windows.UTF16ToString(u), nil
}
