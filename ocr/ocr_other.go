//go:build !darwin && !windows

package ocr

import "image"

func recognize(image.Image, Options) ([]Line, error) { return nil, ErrUnsupported }
