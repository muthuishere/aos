package ocr

import (
	"errors"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/wslhost"
)

func fakeWSL(files map[string]bool) wslhost.Deps {
	return wslhost.Deps{
		Env:    func(string) string { return "" },
		Exists: func(p string) bool { return files[p] },
		Output: func(_, name string, args ...string) (string, error) {
			switch {
			case name == "cmd.exe":
				return "C:\\Users\\me\\AppData\\Local", nil
			case name == "wslpath" && args[0] == "-u":
				return "/mnt/c/Users/me/AppData/Local", nil
			case name == "wslpath" && args[0] == "-w":
				return "\\\\wsl.localhost\\Ubuntu" + strings.ReplaceAll(args[1], "/", "\\"), nil
			}
			return "", errors.New("unexpected " + name)
		},
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
	}
}

func TestHopAOSRunsWindowsOCR(t *testing.T) {
	exe := "/mnt/c/Users/me/AppData/Local/aos/aos.exe"
	d := fakeWSL(map[string]bool{exe: true, "/home/me/shot.png": true})
	var gotExe string
	var gotArgs []string
	run := func(e string, args []string) ([]byte, error) {
		gotExe, gotArgs = e, args
		return []byte(`[{"text":"Hello world","x":10,"y":20,"w":100,"h":18,"confidence":0,"words":[{"text":"Hello","x":10,"y":20,"w":45,"h":18}]}]`), nil
	}
	lines, err := hopAOS(d, run, "/home/me/shot.png", Options{Languages: []string{"en-US"}, Fast: true})
	if err != nil {
		t.Fatal(err)
	}
	if gotExe != exe || strings.Join(gotArgs, " ") != `ocr \\wsl.localhost\Ubuntu\home\me\shot.png --json --lang=en-US --fast` {
		t.Fatalf("ran %s %q", gotExe, gotArgs)
	}
	if len(lines) != 1 || lines[0].Text != "Hello world" || lines[0].W != 100 || lines[0].Words[0].Text != "Hello" {
		t.Fatalf("lines %+v", lines)
	}
}

func TestHopAOSWithoutAOSExeIsUnsupported(t *testing.T) {
	d := fakeWSL(map[string]bool{"/home/me/shot.png": true})
	_, err := hopAOS(d, nil, "/home/me/shot.png", Options{})
	if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "install aos on Windows") {
		t.Fatalf("got %v", err)
	}
}

func TestHopAOSOldAOSExe(t *testing.T) {
	exe := "/mnt/c/Users/me/AppData/Local/aos/aos.exe"
	d := fakeWSL(map[string]bool{exe: true, "/home/me/shot.png": true})
	run := func(string, []string) ([]byte, error) {
		return []byte("aos: unknown command \"ocr\"\n"), errors.New("exit status 1")
	}
	if _, err := hopAOS(d, run, "/home/me/shot.png", Options{}); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "update") {
		t.Fatalf("got %v", err)
	}
}
