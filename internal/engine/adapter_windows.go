//go:build windows

package engine

import winadapter "github.com/muthuishere/aos/internal/engine/adapter/windows"

func newPlatformAdapter() Adapter { return winadapter.New() }
