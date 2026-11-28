//go:build !darwin && !windows && !linux

package ocr

import "image"

func recognize(image.Image, Options) ([]Line, error) { return nil, ErrUnsupported }
