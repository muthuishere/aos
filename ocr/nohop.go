//go:build !linux

package ocr

func wslFile(string, Options) ([]Line, error, bool) { return nil, nil, false }
