package groups

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// trashInfo is what `trash size --json` emits.
type trashInfo struct {
	Items int    `json:"items"`
	Bytes uint64 `json:"bytes"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("trash", "The Trash / Recycle Bin: how much it holds, and emptying it")
		r.Add(
			&cli.Command{
				Group: "trash", Name: "size",
				Summary: "Print how many items the Trash holds and how much space they take",
				Args:    "[--json]",
				Examples: []string{
					"aos trash size",
					"aos trash size --json",
				},
				Run: runTrashSize,
			},
			&cli.Command{
				Group: "trash", Name: "empty",
				Summary: "Permanently delete everything in the Trash (asks first unless --yes)",
				Args:    "[--yes]",
				Examples: []string{
					"aos trash empty          # asks y/N at a terminal",
					"aos trash empty --yes    # no question; required when not at a terminal",
				},
				Run: runTrashEmpty,
			},
		)
	})
}

func runTrashSize(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("trash size", args)
	if err != nil {
		return err
	}
	info, err := readTrash()
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(c, info)
	}
	c.Println(describeTrash(info))
	return nil
}

func runTrashEmpty(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("yes", "y"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`trash empty` takes no arguments")
	}
	info, err := readTrash()
	if err != nil {
		return err
	}
	if info.Items == 0 {
		c.Println("trash is already empty")
		return nil
	}
	if !set.Has("yes") && !set.Has("y") {
		ok, err := confirm(c, fmt.Sprintf("Permanently delete %s?", describeTrash(info)), stdinIsTerminal())
		if err != nil {
			return err
		}
		if !ok {
			return &cli.ExitError{Code: 1, Message: "trash not emptied"}
		}
	}
	if err := emptyTrash(); err != nil {
		return err
	}
	c.Printf("emptied %s\n", describeTrash(info))
	return nil
}

func describeTrash(t trashInfo) string {
	if t.Items == 0 {
		return "trash is empty"
	}
	noun := "items"
	if t.Items == 1 {
		noun = "item"
	}
	return fmt.Sprintf("%d %s, %s", t.Items, noun, humanBytes(t.Bytes))
}

// confirm asks a y/N question. Away from a terminal (an agent, MCP, a pipe)
// there is nobody to answer, so it refuses and names the flag instead of
// reading whatever happens to be on stdin.
func confirm(c *cli.Ctx, question string, interactive bool) (bool, error) {
	if !interactive {
		return false, &cli.ExitError{Code: 1, Message: "this deletes data and needs a yes: re-run with --yes once the person has agreed"}
	}
	fmt.Fprintf(c.Stderr, "%s [y/N] ", question)
	line, err := bufio.NewReader(c.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// dirUsage counts the top-level entries of dir (what the person sees in the
// Trash) and the bytes of every file beneath it. Unreadable parts are skipped.
func dirUsage(dir string) (trashInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return trashInfo{}, nil
		}
		return trashInfo{}, err
	}
	var t trashInfo
	for _, e := range entries {
		if e.Name() == ".DS_Store" {
			continue
		}
		t.Items++
		_ = filepath.WalkDir(filepath.Join(dir, e.Name()), func(_ string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.Type().IsRegular() {
				if fi, err := d.Info(); err == nil {
					t.Bytes += uint64(fi.Size())
				}
			}
			return nil
		})
	}
	return t, nil
}

// clearDir removes everything inside dir, keeping dir itself.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
