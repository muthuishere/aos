//go:build darwin

package engine

import darwinadapter "github.com/muthuishere/aos/internal/engine/adapter/darwin"

func newPlatformAdapter() Adapter { return darwinadapter.New() }
