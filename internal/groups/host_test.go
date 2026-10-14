package groups

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/muthuishere/aos/internal/cli"
)

// Every shipped command must say whether it runs on the host (the screen's
// machine) or always locally. A new group that skips hostTable fails here
// instead of silently acting on the wrong machine under WSL or SSH.
func TestEveryCommandIsClassifiedForTheHost(t *testing.T) {
	r := cli.NewRegistry()
	Register(r)
	for _, cmd := range r.Commands() {
		if _, known := cli.HostSide(cmd.Route()); !known {
			t.Errorf("%s: not in hostTable (internal/cli/host.go); decide whether it follows the screen", cmd.Route())
		}
	}
	for _, route := range []string{"system logout", "system restart", "system shutdown"} {
		if follows, _ := cli.HostSide(route); follows {
			t.Errorf("%s must never be forwarded", route)
		}
	}
}

// A screen command delegated over a forwarded socket comes back with its
// stdout, exit code, and the file it wrote landing at the caller's path.
func TestSocketHostRoundTrip(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "aos-sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s.sock")

	// The host: a registry with one screen command that writes --out.
	host := cli.NewRegistry()
	host.Describe("window", "Windows")
	host.Describe("exec", "Exec")
	host.Add(&cli.Command{Group: "window", Name: "shot", Summary: "s", Run: func(c *cli.Ctx, args []string) error {
		set, err := parseArgs(args)
		if err != nil {
			return err
		}
		out := set.String("out", "")
		if err := os.WriteFile(out, []byte("PNGDATA"), 0o644); err != nil {
			return err
		}
		c.Printf("saved %s\n", out)
		c.Warnf("on the host\n")
		return &cli.ExitError{Code: 4}
	}}, &cli.Command{Group: "exec", Name: "run", Summary: "s", Run: func(c *cli.Ctx, args []string) error { return nil }})
	var hostOut bytes.Buffer
	hostCtx := &cli.Ctx{Registry: host, Stdout: &hostOut, Stderr: &hostOut, Env: func(string) string { return "" }, GOOS: "darwin", Version: "test"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set, _ := parseArgs([]string{"--quiet", "--gui=on"}, "gui")
	go func() { _ = serveSocket(ctx, hostCtx, set, socket) }()
	for i := 0; i < 100; i++ {
		if info, err := os.Stat(socket); err == nil {
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("socket mode %v, want 0600", info.Mode().Perm())
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The caller: same command name, run with the socket as host.
	caller := cli.NewRegistry()
	caller.Describe("window", "Windows")
	caller.Describe("exec", "Exec")
	caller.Add(&cli.Command{Group: "window", Name: "shot", Summary: "s", Run: func(c *cli.Ctx, args []string) error {
		t.Error("ran locally")
		return nil
	}})
	local := filepath.Join(dir, "local.png")
	var out, errOut bytes.Buffer
	callerCtx := &cli.Ctx{Registry: caller, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
		Env: func(k string) string {
			if k == "AOS_HOST" {
				return socket
			}
			return ""
		}, GOOS: "linux"}

	code := cli.Run(callerCtx, []string{"window", "shot", "--out=" + local})
	if code != 4 {
		t.Fatalf("exit %d, want 4; stderr %q", code, errOut.String())
	}
	if out.String() != "saved "+local+"\n" {
		t.Fatalf("stdout %q", out.String())
	}
	if !strings.Contains(errOut.String(), "on the host") {
		t.Fatalf("stderr %q", errOut.String())
	}
	if data, err := os.ReadFile(local); err != nil || string(data) != "PNGDATA" {
		t.Fatalf("file %q %v", data, err)
	}

	// Commands about the host's own machine are refused to a remote caller.
	resp := cli.ServeSocketInvoke(hostCtx, cli.SocketRequest{Argv: []string{"exec", "run", "id"}})
	if resp.Exit == 0 {
		t.Fatal("exec was run for a remote caller")
	}
}

// The socket must not be remote exec: no spelling of the host flag, no
// unresolvable route, and no local command gets through aos_invoke, and the
// server lists no tool for a local command.
func TestSocketHostRefusesLocalCommands(t *testing.T) {
	ran := map[string]int{}
	record := func(route string) cli.Runner {
		return func(c *cli.Ctx, args []string) error { ran[route]++; return nil }
	}
	host := cli.NewRegistry()
	for _, g := range []string{"window", "exec", "system", "capture", "subtitle"} {
		host.Describe(g, g)
	}
	host.Add(
		&cli.Command{Group: "window", Name: "list", Summary: "s", Run: record("window list")},
		&cli.Command{Group: "exec", Name: "capture", Summary: "s", Run: record("exec capture")},
		&cli.Command{Group: "system", Name: "restart", Summary: "s", Run: record("system restart")},
		&cli.Command{Group: "capture", Name: "screenshot", Summary: "s", Aliases: []string{"screenshot"}, Run: record("capture screenshot")},
		&cli.Command{Group: "subtitle", Name: "show", Summary: "s", Run: record("subtitle show")},
	)
	var sink bytes.Buffer
	hostCtx := &cli.Ctx{Registry: host, Stdout: &sink, Stderr: &sink, Env: func(string) string { return "" }, GOOS: "darwin"}

	for _, argv := range [][]string{
		{"--host=local", "exec", "capture", "echo", "PWNED"},
		{"--host", "local", "exec", "capture", "echo", "PWNED"},
		{"--host=local", "system", "restart"},
		{"system", "restart"},
		{"nosuch", "command"},
		{"--host"},
	} {
		if resp := cli.ServeSocketInvoke(hostCtx, cli.SocketRequest{Argv: argv}); resp.Exit == 0 {
			t.Errorf("%v: exit 0, want refused", argv)
		}
	}
	if ran["exec capture"]+ran["system restart"] != 0 {
		t.Fatalf("local commands ran for a remote caller: %v", ran)
	}

	// The caption follows the resolved route: the screenshot alias is a
	// capture, so no caption lands in it; window list gets one.
	cli.ServeSocketInvoke(hostCtx, cli.SocketRequest{Argv: []string{"screenshot"}, From: "alias-test"})
	if ran["capture screenshot"] != 1 || ran["subtitle show"] != 0 {
		t.Fatalf("alias: %v", ran)
	}
	cli.ServeSocketInvoke(hostCtx, cli.SocketRequest{Argv: []string{"window", "list"}, From: "alias-test"})
	if ran["subtitle show"] != 1 {
		t.Fatalf("caption not shown for window list: %v", ran)
	}

	// Tools: only screen commands, plus aos_invoke.
	dir, err := os.MkdirTemp("/tmp", "aos-sock")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "s.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	set, _ := parseArgs([]string{"--quiet", "--gui=on"}, "gui")
	go func() { _ = serveSocket(ctx, hostCtx, set, socket) }()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	hc := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", socket)
	}}}
	mc, err := client.NewStreamableHttpClient("http://aos/mcp", transport.WithHTTPBasicClient(hc))
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Close()
	if err := mc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	if _, err := mc.Initialize(ctx, init); err != nil {
		t.Fatal(err)
	}
	list, err := mc.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	got := strings.Join(names, ",")
	for _, banned := range []string{"exec_capture", "system_restart"} {
		if strings.Contains(got, banned) {
			t.Errorf("socket lists %s: %s", banned, got)
		}
	}
	for _, want := range []string{"window_list", cli.SocketInvokeTool} {
		if !strings.Contains(got, want) {
			t.Errorf("socket is missing %s: %s", want, got)
		}
	}
}
