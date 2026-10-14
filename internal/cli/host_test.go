package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeWSL is a WSL machine: env says so, cmd.exe and wslpath answer, and
// aos.exe sits where install.sh puts it.
type fakeWSL struct {
	files    map[string]bool
	execName string
	execArgs []string
	exitCode int
}

func (f *fakeWSL) deps() *HostDeps {
	return &HostDeps{
		Exists: func(p string) bool { return f.files[p] },
		Output: func(dir, name string, args ...string) (string, error) {
			switch {
			case name == "cmd.exe":
				return `C:\Users\me\AppData\Local`, nil
			case name == "wslpath" && args[0] == "-u":
				return "/mnt/c/Users/me/AppData/Local", nil
			case name == "wslpath" && args[0] == "-w":
				return `\\wsl.localhost\Ubuntu` + strings.ReplaceAll(args[1], "/", `\`), nil
			}
			return "", errors.New("unexpected " + name)
		},
		LookPath: func(string) (string, error) { return "", errors.New("not on PATH") },
		Exec: func(c *Ctx, name string, args []string) (int, error) {
			f.execName, f.execArgs = name, args
			c.Printf("from windows\n")
			return f.exitCode, nil
		},
		Ping: func(string) error { return errors.New("no socket") },
	}
}

const fakeExe = "/mnt/c/Users/me/AppData/Local/aos/aos.exe"

func hostCtx(t *testing.T, env map[string]string, deps *HostDeps) (*Ctx, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cfg := t.TempDir()
	r := NewRegistry()
	r.Describe("window", "Windows")
	r.Describe("exec", "Exec")
	r.Describe("system", "System")
	ran := func(c *Ctx, args []string) error { c.Printf("ran locally %v\n", args); return nil }
	r.Add(
		&Command{Group: "window", Name: "list", Summary: "s", Run: ran},
		&Command{Group: "window", Name: "save", Summary: "s", Run: ran},
		&Command{Group: "exec", Name: "run", Summary: "s", Run: ran},
		&Command{Group: "system", Name: "lock", Summary: "s", Run: ran},
		&Command{Group: "system", Name: "shutdown", Summary: "s", Run: ran},
	)
	var out, errOut bytes.Buffer
	c := &Ctx{
		Registry: r, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut, GOOS: "linux",
		Env: func(k string) string {
			if k == "AOS_CONFIG_DIR" {
				return cfg
			}
			if k == "HOME" {
				return "/home/me"
			}
			return env[k]
		},
		HostDeps: deps,
	}
	return c, &out, &errOut
}

func TestCommandsAreClassifiedByTheTable(t *testing.T) {
	c, _, _ := hostCtx(t, nil, nil)
	for route, want := range map[string]bool{
		"window list": true, "exec run": false, "system lock": true, "system shutdown": false,
	} {
		group, name, _ := strings.Cut(route, " ")
		if got := c.Registry.Lookup(group, name).FollowsHost; got != want {
			t.Errorf("%s FollowsHost = %v, want %v", route, got, want)
		}
	}
}

func TestStripHostFlag(t *testing.T) {
	args, host, err := stripHostFlag([]string{"window", "list", "--host=local", "--json"})
	if err != nil || host != "local" || !reflect.DeepEqual(args, []string{"window", "list", "--json"}) {
		t.Fatalf("got %v %q %v", args, host, err)
	}
	args, host, _ = stripHostFlag([]string{"--host", "windows-host", "window", "list"})
	if host != "windows-host" || !reflect.DeepEqual(args, []string{"window", "list"}) {
		t.Fatalf("got %v %q", args, host)
	}
	// After --, everything belongs to the command.
	args, host, _ = stripHostFlag([]string{"exec", "run", "--", "x", "--host=y"})
	if host != "" || len(args) != 5 {
		t.Fatalf("got %v %q", args, host)
	}
}

func TestWSLDelegatesScreenCommandsToAOSExe(t *testing.T) {
	wsl := &fakeWSL{files: map[string]bool{fakeExe: true}, exitCode: 3}
	c, out, _ := hostCtx(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, wsl.deps())

	code := Run(c, []string{"window", "list", "--out=~/s.png", "--host=auto", "--json"})
	if code != 3 {
		t.Fatalf("exit %d, want aos.exe's 3", code)
	}
	if wsl.execName != fakeExe {
		t.Fatalf("ran %q", wsl.execName)
	}
	want := []string{"window", "list", `--out=\\wsl.localhost\Ubuntu\home\me\s.png`, "--json"}
	if !reflect.DeepEqual(wsl.execArgs, want) {
		t.Fatalf("args %q, want %q", wsl.execArgs, want)
	}
	if out.String() != "from windows\n" {
		t.Fatalf("stdout %q", out.String())
	}
	// The found path is cached.
	if got := ReadConfig(c.Env)["windows-exe"]; got != fakeExe {
		t.Fatalf("cached %q", got)
	}
}

func TestWSLKeepsLocalCommandsLocal(t *testing.T) {
	wsl := &fakeWSL{files: map[string]bool{fakeExe: true}}
	c, out, _ := hostCtx(t, map[string]string{"WSL_INTEROP": "/run/x"}, wsl.deps())
	for _, argv := range [][]string{{"exec", "run", "ls"}, {"system", "shutdown"}, {"window", "list", "--host=local"}} {
		out.Reset()
		if code := Run(c, argv); code != 0 || !strings.HasPrefix(out.String(), "ran locally") {
			t.Fatalf("%v: exit %d, %q", argv, code, out.String())
		}
	}
	if wsl.execName != "" {
		t.Fatalf("aos.exe was run for a local command")
	}
}

func TestWSLPositionalPathsAreTranslated(t *testing.T) {
	wsl := &fakeWSL{files: map[string]bool{fakeExe: true}}
	c, _, _ := hostCtx(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, wsl.deps())
	if code := Run(c, []string{"window", "save", "/home/me/l.json"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if wsl.execArgs[2] != `\\wsl.localhost\Ubuntu\home\me\l.json` {
		t.Fatalf("args %q", wsl.execArgs)
	}
}

func TestMissingAOSExeFailsWithTheFix(t *testing.T) {
	wsl := &fakeWSL{files: map[string]bool{}}
	c, out, errOut := hostCtx(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, wsl.deps())
	if code := Run(c, []string{"window", "list"}); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "install.sh") {
		t.Fatalf("no silent fallback, and the fix named: out=%q err=%q", out.String(), errOut.String())
	}
}

func TestHostPrecedence(t *testing.T) {
	wsl := &fakeWSL{files: map[string]bool{fakeExe: true}}
	c, _, _ := hostCtx(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "AOS_HOST": "local"}, wsl.deps())
	if h, _ := c.ResolveHost(""); h.Kind != HostLocal || h.Via != "AOS_HOST" {
		t.Fatalf("env: %+v", h)
	}
	if h, _ := c.ResolveHost("windows-host"); h.Kind != HostWindowsHost || h.Via != "--host" {
		t.Fatalf("flag: %+v", h)
	}

	c2, _, _ := hostCtx(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, wsl.deps())
	if err := WriteConfigValue(c2.Env, "host", "local"); err != nil {
		t.Fatal(err)
	}
	if h, _ := c2.ResolveHost(""); h.Kind != HostLocal || !strings.HasPrefix(h.Via, "config") {
		t.Fatalf("config: %+v", h)
	}
	if err := WriteConfigValue(c2.Env, "host", "auto"); err != nil {
		t.Fatal(err)
	}
	if h, _ := c2.ResolveHost(""); h.Kind != HostWindowsHost {
		t.Fatalf("auto: %+v", h)
	}
	data, _ := os.ReadFile(ConfigPath(c2.Env))
	if strings.Count(string(data), "host =") != 1 {
		t.Fatalf("config rewritten badly:\n%s", data)
	}
}

func TestSSHUsesTheForwardedSocketOnlyWhenItAnswers(t *testing.T) {
	sock := "/tmp/aos-me.sock"
	answers := false
	deps := &HostDeps{
		Exists: func(p string) bool { return p == sock },
		Ping: func(string) error {
			if answers {
				return nil
			}
			return errors.New("refused")
		},
	}
	c, _, _ := hostCtx(t, map[string]string{"SSH_CONNECTION": "1 2 3 4", "USER": "me"}, deps)
	c.GOOS = "darwin"
	if h, _ := c.ResolveHost(""); h.Kind != HostLocal {
		t.Fatalf("dead socket: %+v", h)
	}
	answers = true
	if h, _ := c.ResolveHost(""); h.Kind != "socket" || h.Socket != sock {
		t.Fatalf("live socket: %+v", h)
	}
	// Asked for explicitly, a dead socket is an error, not local.
	answers = false
	if _, err := c.ResolveHost(sock); err == nil || !strings.Contains(err.Error(), "RemoteForward") {
		t.Fatalf("want the RemoteForward fix, got %v", err)
	}
}

func TestSocketPlan(t *testing.T) {
	env := func(k string) string {
		if k == "HOME" {
			return "/home/me"
		}
		return ""
	}
	exists := func(p string) bool { return p == "/home/me/l.json" }
	shot := &Command{Group: "capture", Name: "screenshot"}
	args, outs, err := socketPlan(shot, []string{"--monitor=1", "--out=~/s.png"}, env, exists)
	if err != nil || args[1] != "--out=@aos-out-0@" || outs["@aos-out-0@"] != filepath.Join("/home/me", "s.png") {
		t.Fatalf("%v %v %v", args, outs, err)
	}
	if _, _, err := socketPlan(&Command{Group: "window", Name: "arrange"}, []string{"/home/me/l.json"}, env, exists); err == nil || !strings.Contains(err.Error(), "window arrange -") {
		t.Fatalf("input file: %v", err)
	}
	if _, _, err := socketPlan(&Command{Group: "watch", Name: "clipboard"}, nil, env, exists); err == nil {
		t.Fatal("streams must be refused")
	}
	if _, _, err := socketPlan(&Command{Group: "capture", Name: "video start"}, []string{"--out=x.mp4"}, env, exists); err == nil {
		t.Fatal("video output must be refused")
	}
}
