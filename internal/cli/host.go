package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The host is the machine whose screen the user is looking at (ADR 0001).
// Screen commands run there; commands about the agent's own machine run here.

const (
	HostLocal       = "local"
	HostWindowsHost = "windows-host"
	HostAuto        = "auto"
)

// hostTable says which side each command belongs on. Keys are a group, or a
// full route that overrides its group. true follows the host; false is always
// local. Every builtin command must be covered (see TestEveryCommandClassified),
// which is what makes a new group declare its side.
var hostTable = map[string]bool{
	// Screen, input and the person sitting at it.
	"window": true, "display": true, "mouse": true, "key": true, "find": true,
	"draw": true, "subtitle": true, "capture": true, "tab": true,
	"clipboard": true, "watch clipboard": true, "watch window": true, "watch tab": true,
	"launch": true, "open": true, "webapp": true,
	"remote": true, "audio": true, "permission": true, "font": true,
	// lock and sleep mean "lock *my* screen". logout/restart/shutdown are
	// never forwarded: taking down the wrong machine is not recoverable.
	"system lock": true, "system sleep": true,

	// The agent's own machine.
	"exec": false, "process": false, "file": false, "pkg": false, "service": false,
	"watch file": false, "network": false, "battery": false, "power": false,
	"system": false, "skill": false, "adapters": false, "obs": false,
	"serve": false, "headless": false, "debug": false, "completion": false,
	"version": false, "doctor": false, "install": false, "uninstall": false,
}

// HostSide looks a route up in the table: follows reports the side, known
// whether the route (or its group) is classified at all.
func HostSide(route string) (follows, known bool) {
	words := strings.Fields(route)
	for n := len(words); n >= 1; n-- {
		if v, ok := hostTable[strings.Join(words[:n], " ")]; ok {
			return v, true
		}
	}
	return false, false
}

// Host is where a screen command is sent.
type Host struct {
	Kind   string // HostLocal, HostWindowsHost, or "socket"
	Socket string // the socket path when Kind is "socket"
	Exe    string // aos.exe, as a WSL path, when Kind is HostWindowsHost
	Via    string // which rule chose it: flag, AOS_HOST, config, auto
}

func (h Host) String() string {
	switch h.Kind {
	case HostWindowsHost:
		return fmt.Sprintf("windows-host (%s, via %s)", h.Exe, h.Via)
	case "socket":
		return fmt.Sprintf("%s (via %s)", h.Socket, h.Via)
	}
	return fmt.Sprintf("local (via %s)", h.Via)
}

// HostDeps is everything host resolution touches outside the process, so tests
// can fake WSL, wslpath and aos.exe.
type HostDeps struct {
	Exists   func(path string) bool
	Output   func(dir, name string, args ...string) (string, error)
	LookPath func(name string) (string, error)
	// Exec runs a program with the given stdio and returns its exit code.
	Exec func(c *Ctx, name string, args []string) (int, error)
	// Ping reports whether a forwarded aos socket answers.
	Ping func(socket string) error
}

func defaultHostDeps() *HostDeps {
	return &HostDeps{
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		Output: func(dir, name string, args ...string) (string, error) {
			cmd := exec.Command(name, args...)
			if dir != "" {
				cmd.Dir = dir
			}
			out, err := cmd.Output()
			return strings.TrimSpace(strings.ReplaceAll(string(out), "\r", "")), err
		},
		LookPath: exec.LookPath,
		Exec:     execPassthrough,
		Ping:     pingSocket,
	}
}

func (c *Ctx) hostDeps() *HostDeps {
	if c.HostDeps == nil {
		c.HostDeps = defaultHostDeps()
	}
	return c.HostDeps
}

// IsWSL reports whether this process runs inside WSL.
func IsWSL(env func(string) string, exists func(string) bool) bool {
	if env("WSL_DISTRO_NAME") != "" || env("WSL_INTEROP") != "" {
		return true
	}
	return exists != nil && exists("/proc/sys/fs/binfmt_misc/WSLInterop")
}

// stripHostFlag removes --host=<v> and --host <v> from args. With anywhere
// false only flags before the first other word are taken, which is how the
// global flag is read before the command is known; dispatch then strips the
// rest of a screen command's arguments with anywhere true. Nothing after --
// is touched. The flag is never forwarded: the host has no host of its own.
func stripHostFlag(args []string, anywhere bool) ([]string, string, error) {
	var out []string
	value := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" || (!anywhere && arg != "--host" && !strings.HasPrefix(arg, "--host=")) {
			return append(out, args[i:]...), value, nil
		}
		switch {
		case strings.HasPrefix(arg, "--host="):
			value = strings.TrimPrefix(arg, "--host=")
		case arg == "--host":
			if i+1 >= len(args) {
				return nil, "", fmt.Errorf("--host needs a value: local, windows-host, or a socket path")
			}
			value = args[i+1]
			i++
		default:
			out = append(out, arg)
		}
	}
	return out, value, nil
}

// ConfigPath is the aos config file: `key = value` lines, # comments.
func ConfigPath(env func(string) string) string {
	return filepath.Join(ConfigDir(env), "config")
}

// ReadConfig returns the config's keys; a missing file is an empty config.
func ReadConfig(env func(string) string) map[string]string {
	values := map[string]string{}
	f, err := os.Open(ConfigPath(env))
	if err != nil {
		return values
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

// WriteConfigValue sets one key, keeping every other line as it was.
func WriteConfigValue(env func(string) string, key, value string) error {
	path := ConfigPath(env)
	data, _ := os.ReadFile(path)
	var lines []string
	replaced := false
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			line = key + " = " + value
			replaced = true
		}
		if line != "" || len(lines) > 0 {
			lines = append(lines, line)
		}
	}
	if !replaced {
		lines = append(lines, key+" = "+value)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

// ResolveHost picks the host: the --host flag, then AOS_HOST, then `host =` in
// the config, then auto-detection. A host that was asked for but cannot be
// reached is an error with the fix, never a silent fallback to local.
func (c *Ctx) ResolveHost(flag string) (Host, error) {
	value, via := flag, "--host"
	if value == "" {
		value, via = strings.TrimSpace(c.Env("AOS_HOST")), "AOS_HOST"
	}
	if value == "" {
		value, via = ReadConfig(c.Env)["host"], "config "+ConfigPath(c.Env)
	}
	if value == "" || value == HostAuto {
		return c.autoHost()
	}
	switch value {
	case HostLocal:
		return Host{Kind: HostLocal, Via: via}, nil
	case HostWindowsHost:
		if c.GOOS != "linux" || !IsWSL(c.Env, c.hostDeps().Exists) {
			return Host{}, fmt.Errorf("host windows-host (from %s) only works inside WSL; use local or a socket path", via)
		}
		exe, err := c.findWindowsAOS()
		if err != nil {
			return Host{}, err
		}
		return Host{Kind: HostWindowsHost, Exe: exe, Via: via}, nil
	}
	socket := strings.TrimPrefix(value, "unix://")
	if strings.HasPrefix(socket, "/") || strings.HasPrefix(socket, "~") {
		socket = expandHome(c.Env, socket)
		if err := c.hostDeps().Ping(socket); err != nil {
			return Host{}, fmt.Errorf("host %s (from %s) does not answer: %v\n%s", socket, via, err, socketFix())
		}
		return Host{Kind: "socket", Socket: socket, Via: via}, nil
	}
	return Host{}, fmt.Errorf("unknown host %q (from %s): use local, windows-host, auto, or a socket path", value, via)
}

func (c *Ctx) autoHost() (Host, error) {
	deps := c.hostDeps()
	if c.GOOS == "linux" && IsWSL(c.Env, deps.Exists) {
		exe, err := c.findWindowsAOS()
		if err != nil {
			return Host{}, err
		}
		return Host{Kind: HostWindowsHost, Exe: exe, Via: "auto: WSL"}, nil
	}
	if c.Env("SSH_CONNECTION") != "" {
		socket := DefaultSocketPath(c.Env)
		if deps.Exists(socket) {
			if err := deps.Ping(socket); err == nil {
				return Host{Kind: "socket", Socket: socket, Via: "auto: SSH"}, nil
			}
		}
	}
	return Host{Kind: HostLocal, Via: "auto"}, nil
}

// DefaultSocketPath is where an SSH RemoteForward puts the host's aos socket.
func DefaultSocketPath(env func(string) string) string {
	user := env("USER")
	if user == "" {
		user = env("LOGNAME")
	}
	return "/tmp/aos-" + user + ".sock"
}

func socketFix() string {
	return "Fix: on the machine with the screen run `aos serve mcp --socket ~/.aos/serve.sock`, and add to ~/.ssh/config there:\n" +
		"    RemoteForward /tmp/aos-%r.sock %d/.aos/serve.sock\n" +
		"or pass --host=local to act on this machine."
}

func expandHome(env func(string) string, p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home := env("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

// findWindowsAOS locates aos.exe from inside WSL: the cached path in the
// config, then %LOCALAPPDATA%\aos (where install.sh puts it),
// %LOCALAPPDATA%\Programs\aos (install.cmd's default), then PATH.
func (c *Ctx) findWindowsAOS() (string, error) {
	deps := c.hostDeps()
	if cached := ReadConfig(c.Env)["windows-exe"]; cached != "" && deps.Exists(cached) {
		return cached, nil
	}
	var tried []string
	// cmd.exe complains about a UNC working directory, so run it from C:.
	local, err := deps.Output("/mnt/c", "cmd.exe", "/c", "echo %LOCALAPPDATA%")
	if err != nil || local == "" || strings.Contains(local, "%") {
		return "", fmt.Errorf("aos is running in WSL, so screen commands go to Windows, but Windows programs cannot be started from here (cmd.exe: %v)\n"+
			"Fix: enable interop in /etc/wsl.conf ([interop] enabled=true), then `wsl.exe --shutdown`; or pass --host=local to act on WSL itself", err)
	}
	if dir, err := deps.Output("", "wslpath", "-u", local); err == nil && dir != "" {
		for _, candidate := range []string{
			filepath.Join(dir, "aos", "aos.exe"),
			filepath.Join(dir, "Programs", "aos", "aos.exe"),
		} {
			tried = append(tried, candidate)
			if deps.Exists(candidate) {
				_ = WriteConfigValue(c.Env, "windows-exe", candidate)
				return candidate, nil
			}
		}
	}
	if found, err := deps.LookPath("aos.exe"); err == nil {
		_ = WriteConfigValue(c.Env, "windows-exe", found)
		return found, nil
	}
	tried = append(tried, "aos.exe on PATH")
	return "", fmt.Errorf("aos is running in WSL, so screen commands go to Windows, but aos.exe was not found (tried %s)\n"+
		"Fix: re-run install.sh inside WSL (it installs the Windows build into %%LOCALAPPDATA%%\\aos), "+
		"or run install.cmd on Windows; or pass --host=local to act on WSL itself", strings.Join(tried, ", "))
}

// Path-valued flags, and the routes whose positional arguments are paths. These
// are translated for aos.exe, and moved across for a socket host.
var (
	pathFlags  = map[string]bool{"out": true, "preview": true, "events": true, "file": true}
	pathRoutes = map[string]bool{"window arrange": true, "window save": true, "open": true, "capture video zoom": true}
	// routePathFlags are path flags only on one route: --cwd is a folder for
	// `tab open`, but other commands may use the name differently.
	routePathFlags = map[string]map[string]bool{"tab open": {"cwd": true}}
)

func isPathFlag(route, name string) bool {
	return pathFlags[name] || routePathFlags[route][name]
}

// toWindowsArgs rewrites path arguments into Windows paths with wslpath -w, so
// output files land back in WSL.
func (c *Ctx) toWindowsArgs(cmd *Command, args []string) ([]string, error) {
	out := make([]string, 0, len(args))
	positional := pathRoutes[cmd.Route()]
	for _, arg := range args {
		if name, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(name, "--") && isPathFlag(cmd.Route(), strings.TrimPrefix(name, "--")) && value != "" {
			win, err := c.wslToWindows(value)
			if err != nil {
				return nil, err
			}
			out = append(out, name+"="+win)
			continue
		}
		if positional && !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "://") &&
			(cmd.Route() != "open" || c.hostDeps().Exists(expandHome(c.Env, arg))) {
			win, err := c.wslToWindows(arg)
			if err != nil {
				return nil, err
			}
			out = append(out, win)
			continue
		}
		out = append(out, arg)
	}
	return out, nil
}

// wslToWindows converts a WSL path, which may not exist yet, to a Windows path.
// wslpath only converts paths that exist, so the parent is converted and the
// file name appended.
func (c *Ctx) wslToWindows(p string) (string, error) {
	p = expandHome(c.Env, p)
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	deps := c.hostDeps()
	if deps.Exists(p) {
		if win, err := deps.Output("", "wslpath", "-w", p); err == nil && win != "" {
			return win, nil
		}
	}
	dir, base := filepath.Split(p)
	win, err := deps.Output("", "wslpath", "-w", filepath.Clean(dir))
	if err != nil || win == "" {
		return "", fmt.Errorf("cannot translate %s for Windows (wslpath: %v); does the folder exist?", p, err)
	}
	return strings.TrimRight(win, `\`) + `\` + base, nil
}

// runOnHost sends a screen command to a host that is not this machine.
func (c *Ctx) runOnHost(host Host, cmd *Command, words, rest []string) int {
	if host.Kind == HostWindowsHost {
		return c.runOnWindowsHost(host, cmd, words, rest)
	}
	return c.runOnSocket(host, cmd, words, rest)
}

// runOnWindowsHost runs the command through aos.exe with the same arguments.
// stdio passes through and the exit code is aos.exe's own.
func (c *Ctx) runOnWindowsHost(host Host, cmd *Command, words, rest []string) int {
	translated, err := c.toWindowsArgs(cmd, rest)
	if err != nil {
		c.Warnf("aos: %v\n", err)
		return 1
	}
	argv := append(append([]string{}, words...), translated...)
	code, err := c.hostDeps().Exec(c, host.Exe, argv)
	if err != nil {
		c.Warnf("aos: could not start %s on the Windows host: %v\n", host.Exe, err)
		c.Warnf("Fix: re-run install.sh inside WSL to reinstall aos.exe, or pass --host=local to act on WSL itself.\n")
		return 1
	}
	return code
}

func execPassthrough(c *Ctx, name string, args []string) (int, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), nil
	}
	if err != nil {
		return 0, err
	}
	return 0, nil
}
