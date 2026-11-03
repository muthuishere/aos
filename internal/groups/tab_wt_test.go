package groups

import (
	"testing"

	"github.com/muthuishere/aos/internal/engine"
)

func TestMatchShellsTitleThenOrder(t *testing.T) {
	tabs := []Tab{
		{Window: "A", Index: 1, Title: "cmd.exe"},
		{Window: "B", Index: 1, Title: "build"},
		{Window: "B", Index: 2, Title: "cmd.exe"},
	}
	shells := []engine.ShellInfo{ // creation order
		{PID: 10, Title: "cmd.exe", CWD: `C:\a`},
		{PID: 11, Title: "build", CWD: `C:\b`},
		{PID: 12, Title: "cmd.exe", CWD: `C:\c`},
	}
	matchShells(tabs, []int{0, 1, 2}, shells)
	if tabs[1].PID != 11 || tabs[1].Match != "title" || tabs[1].CWD != `C:\b` {
		t.Fatalf("unique title: %+v", tabs[1])
	}
	// "cmd.exe" is on two shells: not a title match; order decides.
	if tabs[0].PID != 10 || tabs[0].Match != "order" || tabs[2].PID != 12 || tabs[2].Match != "order" {
		t.Fatalf("order: %+v %+v", tabs[0], tabs[2])
	}
}

func TestMatchShellsMoreTabsThanShells(t *testing.T) {
	tabs := []Tab{{Window: "A", Index: 1, Title: "x"}, {Window: "A", Index: 2, Title: "y"}}
	matchShells(tabs, []int{0, 1}, []engine.ShellInfo{{PID: 5, Title: "y"}})
	if tabs[1].PID != 5 || tabs[0].PID != 0 {
		t.Fatalf("%+v", tabs)
	}
}
