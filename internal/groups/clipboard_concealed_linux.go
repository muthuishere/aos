package groups

import (
	"github.com/muthuishere/aos/internal/sys"
)

// clipboardConcealed reports whether a password manager marked the selection
// with KDE's hint. It is only asked when the text changed.
func clipboardConcealed() bool {
	var out string
	var err error
	switch {
	case sys.Has("wl-paste"):
		out, err = sys.Output("wl-paste", "--list-types")
	case sys.Has("xclip"):
		out, err = sys.Output("xclip", "-selection", "clipboard", "-t", "TARGETS", "-out")
	default:
		return false
	}
	return err == nil && concealedTypes(out)
}
