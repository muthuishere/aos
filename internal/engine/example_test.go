package engine_test

import (
	"fmt"

	"github.com/muthuishere/aos/internal/engine"
)

// Examples in this file double as the importability check for the public
// Go API: they live in package windowctl_test, which means they import
// "github.com/muthuishere/aos/internal/engine" the same way an external consumer
// would. Compilation success demonstrates the requirement from
// docs/specs/07-go-library-api.md that consumers see the documented types
// and functions without pulling in CLI dependencies.

func ExampleListWindows() {
	ws, err := engine.ListWindows(engine.Filter{Title: "chrome"})
	if err != nil {
		return
	}
	for _, w := range ws {
		fmt.Println(w.Title)
	}
}

func ExampleListMonitors() {
	ms, err := engine.ListMonitors()
	if err != nil {
		return
	}
	for _, m := range ms {
		fmt.Printf("monitor %d: %dx%d\n", m.ID, m.Width, m.Height)
	}
}

func ExampleMoveZone() {
	monitorID := 1
	_ = engine.MoveZone(
		engine.Match{Title: "chrome"},
		&monitorID,
		"2B",
	)
}

func ExampleMoveCoords() {
	_ = engine.MoveCoords(
		engine.Match{Title: "chrome"},
		nil,
		engine.Rect{X: 0, Y: 0, W: 960, H: 1080},
	)
}

func ExampleFocus() {
	_ = engine.Focus(engine.Match{Title: "jira"})
}
