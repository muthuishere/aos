package groups

import "time"

// Windows Focus (Focus assist / Do not disturb) has no documented API to read
// or set; the only routes are undocumented WNF state blobs, which aos does
// not rely on.
func readFocus() (focusState, error) {
	return focusState{}, focusUnsupported("Windows has no documented API to read Focus / Do not disturb")
}

func setFocus(bool, time.Duration) error {
	return focusUnsupported("Windows has no documented API to set Focus / Do not disturb; use the notification center")
}
