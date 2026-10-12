//go:build linux

package engine

import linuxadapter "github.com/muthuishere/aos/internal/engine/adapter/linux"

func newPlatformAdapter() Adapter { return linuxadapter.New() }
