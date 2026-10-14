package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// SocketInvokeTool is the MCP tool `aos serve mcp --socket` adds for another
// aos to call. Unlike the per-command tools it carries the exit code, stderr,
// and the files the command wrote, so a command delegated over SSH behaves
// exactly as it would locally.
const SocketInvokeTool = "aos_invoke"

// SocketRequest is the aos_invoke argument. Outputs maps a placeholder that
// stands in argv for an output path to the file extension it needs.
type SocketRequest struct {
	Argv    []string          `json:"argv"`
	Stdin   string            `json:"stdin,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
	From    string            `json:"from,omitempty"`
}

// SocketResponse is what aos_invoke returns, as JSON text. Files are base64,
// keyed by placeholder.
type SocketResponse struct {
	Exit   int               `json:"exit"`
	Stdout string            `json:"stdout"`
	Stderr string            `json:"stderr"`
	Files  map[string]string `json:"files,omitempty"`
}

func dialSocket(ctx context.Context, socket string) (*client.Client, error) {
	httpClient := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}
	mc, err := client.NewStreamableHttpClient("http://aos/mcp", transport.WithHTTPBasicClient(httpClient))
	if err != nil {
		return nil, err
	}
	if err := mc.Start(ctx); err != nil {
		return nil, err
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "aos", Version: "client"}
	if _, err := mc.Initialize(ctx, init); err != nil {
		mc.Close()
		return nil, err
	}
	return mc, nil
}

// pingSocket checks that an aos MCP server answers on the socket.
func pingSocket(socket string) error {
	if err := checkSocket(socket); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	mc, err := dialSocket(ctx, socket)
	if err != nil {
		return err
	}
	return mc.Close()
}

// socketPlan rewrites a command's arguments for a host that cannot see this
// machine's files: output paths become placeholders the host fills, and input
// paths are refused with the alternative.
func socketPlan(cmd *Command, rest []string, env func(string) string, exists func(string) bool) ([]string, map[string]string, error) {
	route := cmd.Route()
	if err := refuseStreaming(cmd); err != nil {
		return nil, nil, err
	}
	outputs := map[string]string{} // placeholder -> local path
	placeholder := func(local string) string {
		token := fmt.Sprintf("@aos-out-%d@", len(outputs))
		outputs[token] = expandHome(env, local)
		return token
	}
	out := make([]string, 0, len(rest))
	for _, arg := range rest {
		if name, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(name, "--") && value != "" {
			switch strings.TrimPrefix(name, "--") {
			case "out", "preview":
				if strings.HasPrefix(route, "capture video") {
					return nil, nil, fmt.Errorf("%q over a socket host: the video stays on the machine with the screen; drop %s and copy it from there", route, name)
				}
				out = append(out, name+"="+placeholder(value))
				continue
			case "events", "file":
				return nil, nil, fmt.Errorf("%s=%s is a file on this machine, which the host cannot read over the socket", name, value)
			}
		}
		if pathRoutes[route] && !strings.HasPrefix(arg, "-") && !strings.Contains(arg, "://") {
			switch route {
			case "window save":
				out = append(out, placeholder(arg))
				continue
			case "open":
				if !exists(expandHome(env, arg)) {
					break
				}
				fallthrough
			default:
				hint := ""
				if route == "window arrange" {
					hint = " Pipe it instead: aos window arrange - < " + arg
				}
				return nil, nil, fmt.Errorf("%s is a file on this machine, which the host cannot read over the socket.%s", arg, hint)
			}
		}
		out = append(out, arg)
	}
	return out, outputs, nil
}

// refuseStreaming rejects commands that run until interrupted: the socket
// carries one request and one reply.
func refuseStreaming(cmd *Command) error {
	route := cmd.Route()
	if cmd.Blocking || route == "watch clipboard" || route == "watch window" || route == "remote share" {
		return fmt.Errorf("%q streams its output, which a socket host does not carry yet; run it on the machine with the screen", route)
	}
	return nil
}

// runOnSocket sends the command to the host's aos through the forwarded
// socket and reproduces its stdout, stderr, exit code and output files here.
func (c *Ctx) runOnSocket(host Host, cmd *Command, words, rest []string) int {
	deps := c.hostDeps()
	argv, outputs, err := socketPlan(cmd, rest, c.Env, deps.Exists)
	if err != nil {
		c.Warnf("aos: %v\n", err)
		return 2
	}
	req := SocketRequest{Argv: append(append([]string{}, words...), argv...), Outputs: map[string]string{}}
	for token, local := range outputs {
		req.Outputs[token] = filepath.Ext(local)
	}
	if f, ok := c.Stdin.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice == 0 {
			data, _ := io.ReadAll(f)
			req.Stdin = string(data)
		}
	} else if c.Stdin != nil {
		data, _ := io.ReadAll(c.Stdin)
		req.Stdin = string(data)
	}
	req.From, _ = os.Hostname()

	resp, err := callSocket(host.Socket, req)
	if err != nil {
		c.Warnf("aos: the host at %s did not answer: %v\n%s\n", host.Socket, err, socketFix())
		return 1
	}
	for token, local := range outputs {
		resp.Stdout = strings.ReplaceAll(resp.Stdout, token, local)
		resp.Stderr = strings.ReplaceAll(resp.Stderr, token, local)
		data, ok := resp.Files[token]
		if !ok {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err == nil {
			err = writeReturnedFile(local, raw)
		}
		if err != nil {
			c.Warnf("aos: could not write %s: %v\n", local, err)
			return 1
		}
	}
	io.WriteString(c.Stdout, resp.Stdout)
	io.WriteString(c.Stderr, resp.Stderr)
	return resp.Exit
}

func callSocket(socket string, req SocketRequest) (SocketResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	mc, err := dialSocket(ctx, socket)
	if err != nil {
		return SocketResponse{}, err
	}
	defer mc.Close()
	call := mcp.CallToolRequest{}
	call.Params.Name = SocketInvokeTool
	call.Params.Arguments = map[string]any{
		"argv": req.Argv, "stdin": req.Stdin, "outputs": req.Outputs, "from": req.From,
	}
	result, err := mc.CallTool(ctx, call)
	if err != nil {
		return SocketResponse{}, err
	}
	var text strings.Builder
	for _, content := range result.Content {
		if t, ok := mcp.AsTextContent(content); ok {
			text.WriteString(t.Text)
		}
	}
	if result.IsError {
		return SocketResponse{}, fmt.Errorf("%s (is the host's aos older than this one?)", strings.TrimSpace(text.String()))
	}
	var resp SocketResponse
	if err := json.Unmarshal([]byte(text.String()), &resp); err != nil {
		return SocketResponse{}, fmt.Errorf("unexpected reply from the host: %v", err)
	}
	return resp, nil
}

// lastCaption throttles the "controlled from" caption to one a minute.
var (
	captionMu   sync.Mutex
	lastCaption = map[string]time.Time{}
)

// ServeSocketInvoke runs a delegated command on this machine (the host). Output
// placeholders become files in a temporary folder, which go back base64.
func ServeSocketInvoke(base *Ctx, req SocketRequest) SocketResponse {
	if len(req.Argv) == 0 {
		return SocketResponse{Exit: 2, Stderr: "aos: empty command\n"}
	}
	// Only screen commands are run for a remote caller; anything else would
	// make the socket remote exec on the person's machine. The host flag is
	// stripped first so it cannot hide the route, and a route that does not
	// resolve is refused rather than handed to dispatch.
	argv, _, err := stripHostFlag(req.Argv, false)
	if err != nil {
		return SocketResponse{Exit: 2, Stderr: fmt.Sprintf("aos: %v\n", err)}
	}
	cmd, _, err := base.Registry.Resolve(argv)
	if err != nil || cmd == nil {
		return SocketResponse{Exit: 2, Stderr: "aos: not a screen command; refused for a remote caller\n"}
	}
	if !cmd.FollowsHost {
		return SocketResponse{Exit: 2, Stderr: fmt.Sprintf("aos: %q is not a screen command and is not run for a remote caller\n", cmd.Route())}
	}
	if err := refuseStreaming(cmd); err != nil {
		return SocketResponse{Exit: 2, Stderr: fmt.Sprintf("aos: %v\n", err)}
	}
	req.Argv = argv
	if req.From != "" && cmd.Group != "capture" && cmd.Group != "find" {
		showControlledCaption(base, req.From)
	}
	screen := *base
	screen.ScreenOnly = true

	dir, err := os.MkdirTemp("", "aos-host-")
	if err != nil {
		return SocketResponse{Exit: 1, Stderr: fmt.Sprintf("aos: %v\n", err)}
	}
	defer os.RemoveAll(dir)
	paths := map[string]string{}
	i := 0
	for token, ext := range req.Outputs {
		if strings.ContainsAny(ext, `/\`) {
			ext = ""
		}
		paths[token] = filepath.Join(dir, fmt.Sprintf("out-%d%s", i, ext))
		i++
	}
	argv = make([]string, len(req.Argv))
	for n, arg := range req.Argv {
		for token, path := range paths {
			arg = strings.ReplaceAll(arg, token, path)
		}
		argv[n] = arg
	}

	result := Invoke(&screen, argv, req.Stdin)
	resp := SocketResponse{Exit: result.Exit, Stdout: result.Stdout, Stderr: result.Stderr, Files: map[string]string{}}
	for token, path := range paths {
		resp.Stdout = strings.ReplaceAll(resp.Stdout, path, token)
		resp.Stderr = strings.ReplaceAll(resp.Stderr, path, token)
		if data, err := os.ReadFile(path); err == nil {
			resp.Files[token] = base64.StdEncoding.EncodeToString(data)
		}
	}
	return resp
}

// showControlledCaption tells the person at the screen that it is being driven
// from elsewhere. It is skipped before captures, so it never lands in one.
func showControlledCaption(base *Ctx, from string) {
	captionMu.Lock()
	if time.Since(lastCaption[from]) < time.Minute {
		captionMu.Unlock()
		return
	}
	lastCaption[from] = time.Now()
	captionMu.Unlock()
	Invoke(base, []string{"subtitle", "show", "aos controlled from " + from, "--seconds=3", "--position=top-right", "--size=S", "--detach"}, "")
}

// checkSocket refuses a socket someone else could have planted: on a shared
// server anyone can create /tmp/aos-<you>.sock first. It must be a socket,
// owned by this user, with no group or other permission bits.
func checkSocket(socket string) error {
	info, err := os.Lstat(socket)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s is not a socket", socket)
	}
	if !ownedByMe(info) {
		return fmt.Errorf("%s is not owned by you; refusing it (someone else may have created it)", socket)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable or writable by others (mode %v); it must be 0600", socket, info.Mode().Perm())
	}
	return nil
}

// writeReturnedFile writes a file the host sent back, 0600, never through a
// symlink: an existing entry is removed and the file created exclusively.
func writeReturnedFile(path string, data []byte) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
