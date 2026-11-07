package groups

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procRegisterClipboardFormatW   = capUser32.NewProc("RegisterClipboardFormatW")
	procIsClipboardFormatAvailable = capUser32.NewProc("IsClipboardFormatAvailable")
)

// clipboardConcealed reports whether the copying app asked clipboard monitors
// and history to leave this content alone, the Windows convention password
// managers follow.
func clipboardConcealed() bool {
	for _, name := range []string{"ExcludeClipboardContentFromMonitorProcessing", "Clipboard Viewer Ignore"} {
		if clipboardFormatPresent(name) {
			return true
		}
	}
	return false
}

func clipboardFormatPresent(name string) bool {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return false
	}
	id, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	if id == 0 {
		return false
	}
	r, _, _ := procIsClipboardFormatAvailable.Call(id)
	return r != 0
}
