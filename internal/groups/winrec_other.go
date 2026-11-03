//go:build !darwin || !cgo

package groups

import "errors"

// Window-only capture (`capture video start windows`) is ScreenCaptureKit,
// so macOS only.
const winRecSupported = false

var errWinRec = errors.New("`windows` mode records with ScreenCaptureKit and is macOS only; use `window` mode here")

func sckWindowSizes([]uint32) ([][2]float64, error)   { return nil, errWinRec }
func sckStart([]uint32, []string, int, float64) error { return errWinRec }
func sckFailure() error                               { return nil }
func sckStop() (int, error)                           { return 0, errWinRec }
