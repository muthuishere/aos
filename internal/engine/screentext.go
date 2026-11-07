package engine

import "errors"

// ScreenTextOptions picks the screen area ReadScreenText OCRs: a region
// (global, or relative to Monitor when both are set), a whole monitor, or,
// with neither, the primary monitor — the same rules as Screenshot.
type ScreenTextOptions struct {
	Region   *Rect
	Monitor  *int
	MaxChars int
}

// ReadScreenText OCRs a screen area and returns its text in reading order,
// in the same shape `window text` gives (Window is left empty).
func ReadScreenText(o ScreenTextOptions) (WindowText, Rect, error) {
	return readScreenTextWith(defaultAdapter, o)
}

func readScreenTextWith(a Adapter, o ScreenTextOptions) (WindowText, Rect, error) {
	r, err := resolveScreenshotRect(a, ScreenshotOptions{Region: o.Region, Monitor: o.Monitor})
	if err != nil {
		return WindowText{}, Rect{}, err
	}
	if r.W <= 0 || r.H <= 0 {
		return WindowText{}, r, errors.New("screen text: the area is empty")
	}
	lines, err := a.RecognizeText(r)
	if err != nil {
		return WindowText{}, r, err
	}
	for i := range lines {
		lines[i].Source, lines[i].Role = "ocr", "text"
	}
	out := WindowText{Source: "ocr", Items: orderText(mergeText(visibleIn(lines, r)))}
	return finish(out, o.MaxChars), r, nil
}
