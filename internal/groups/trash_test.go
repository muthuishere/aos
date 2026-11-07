package groups

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

func TestDirUsageAndClear(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), make([]byte, 100), 0o644)
	os.MkdirAll(filepath.Join(dir, "folder", "deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "folder", "deep", "b.bin"), make([]byte, 50), 0o644)
	os.WriteFile(filepath.Join(dir, ".DS_Store"), make([]byte, 9), 0o644)

	got, err := dirUsage(dir)
	if err != nil || got != (trashInfo{Items: 2, Bytes: 150}) {
		t.Errorf("dirUsage = %+v, %v", got, err)
	}
	if err := clearDir(dir); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("left behind: %v", left)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("clearDir must keep the directory itself")
	}
	if got, err := dirUsage(filepath.Join(dir, "missing")); err != nil || got.Items != 0 {
		t.Errorf("missing trash should read as empty: %+v %v", got, err)
	}
}

func TestConfirm(t *testing.T) {
	ask := func(input string, interactive bool) (bool, error) {
		c := &cli.Ctx{Stdin: strings.NewReader(input), Stderr: &bytes.Buffer{}}
		return confirm(c, "Delete?", interactive)
	}
	if ok, _ := ask("y\n", true); !ok {
		t.Error("y should confirm")
	}
	if ok, _ := ask("YES\n", true); !ok {
		t.Error("YES should confirm")
	}
	if ok, _ := ask("\n", true); ok {
		t.Error("empty answer must default to no")
	}
	ok, err := ask("y\n", false)
	var exit *cli.ExitError
	if ok || !errors.As(err, &exit) || !strings.Contains(exit.Message, "--yes") {
		t.Errorf("non-interactive must refuse and name --yes: %v %v", ok, err)
	}
}

func TestDescribeTrash(t *testing.T) {
	for in, want := range map[trashInfo]string{
		{}:                         "trash is empty",
		{Items: 1, Bytes: 2048}:    "1 item, 2.0 KB",
		{Items: 3, Bytes: 1 << 30}: "3 items, 1.0 GB",
	} {
		if got := describeTrash(in); got != want {
			t.Errorf("describeTrash(%+v) = %q, want %q", in, got, want)
		}
	}
}

func TestTrashEmptyRefusesWithoutYes(t *testing.T) {
	if stdinIsTerminal() {
		t.Skip("stdin is a terminal; the refusal path is for agents")
	}
	info, err := readTrash()
	if err != nil || info.Items == 0 {
		t.Skip("nothing in the trash to protect")
	}
	err = runTrashEmpty(&cli.Ctx{Stdin: strings.NewReader("y\n"), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, nil)
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 1 {
		t.Fatalf("emptying without --yes away from a terminal must refuse, got %v", err)
	}
	if after, _ := readTrash(); after.Items != info.Items {
		t.Fatalf("trash changed: %d -> %d items", info.Items, after.Items)
	}
}
