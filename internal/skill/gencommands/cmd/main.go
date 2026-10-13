// Command gencommands writes the skill's references/commands.md from the
// registry. Usage: go run ./internal/skill/gencommands/cmd [repo-root]
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/muthuishere/aos/internal/skill/gencommands"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	path := filepath.Join(root, gencommands.RelPath)
	if err := os.WriteFile(path, []byte(gencommands.Render()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
