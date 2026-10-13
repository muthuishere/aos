package gencommands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandsMDIsCurrent(t *testing.T) {
	path := filepath.Join("..", "assets", "aos", "references", "commands.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != Render() {
		t.Fatalf("%s is out of date with the registry; run: go generate ./internal/skill/...", path)
	}
}

// The generated index lists everything, but an agent learns how to use a
// command from the hand-written guides, so each public route must appear in
// at least one of them too.
func TestHandWrittenSkillMentionsEveryPublicRoute(t *testing.T) {
	dir := filepath.Join("..", "assets", "aos")
	var all strings.Builder
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(p, ".md") && filepath.Base(p) != "commands.md" {
			b, _ := os.ReadFile(p)
			all.Write(b)
		}
		return nil
	})
	text := all.String()
	for _, line := range strings.Split(Render(), "\n") {
		if r, ok := strings.CutPrefix(line, "### `aos "); ok {
			route := strings.TrimSuffix(r, "`")
			if !strings.Contains(text, route) {
				t.Errorf("hand-written skill never mentions %q", route)
			}
		}
	}
}
