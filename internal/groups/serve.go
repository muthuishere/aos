package groups

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/muthuishere/aos/internal/cli"
	toolnexus "github.com/muthuishere/toolnexus/golang"
)

// defaultServeAddr binds loopback only. Serving this registry is remote control
// of the machine, so reaching it from elsewhere should be a deliberate act
// (an SSH tunnel, or an explicit --addr).
const defaultServeAddr = "127.0.0.1:14320"

func init() {
	register(func(r *cli.Registry) {
		r.Describe("serve", "Expose this machine's commands to other agents")
		r.Add(
			&cli.Command{
				Group: "serve", Name: "mcp",
				Summary: "Serve every command as an MCP tool over streamable HTTP",
				Args:    "[--addr=<host:port> | --socket=<path>] [--token=<k>] [--groups=<a,b>] [--gui=on|off|auto] [--quiet]",
				Examples: []string{
					"aos serve mcp",
					"aos serve mcp --socket=~/.aos/serve.sock   # the host end of an SSH RemoteForward",
					"aos serve mcp --addr=127.0.0.1:9000 --groups=window,capture,exec",
					"aos serve mcp --gui=off   # screenless tools only",
				},
				Run: runServeMCP,
			},
			&cli.Command{
				Group: "serve", Name: "tools",
				Summary:  "Print the MCP tool catalogue this machine would expose",
				Args:     "[--groups=<a,b>]",
				Examples: []string{"aos serve tools --groups=window"},
				Run:      runServeTools,
			},
		)
	})
}

// toolName converts a route into an MCP tool name. MCP names allow only
// [A-Za-z0-9_-], so the spaces that separate route words become underscores:
// `audio output volume` is exposed as `audio_output_volume`.
func toolName(route string) string {
	return strings.ReplaceAll(route, " ", "_")
}

// buildTools turns the command registry into MCP tools. Every command becomes
// one tool taking the same arguments a person would type, so an agent and a
// terminal drive the identical code path.
func buildTools(c *cli.Ctx, only map[string]bool, gui guiMode) []toolnexus.Tool {
	includeGUI := gui == guiOn || (gui == guiAuto && cli.HasDisplay(c.Env, c.GOOS))

	var tools []toolnexus.Tool
	for _, cmd := range c.Registry.Commands() {
		if cmd.Hidden || cmd.Blocking || !cmd.Supports(c.GOOS) {
			continue
		}
		// Offering an agent a tool that cannot run is worse than not offering
		// it: it will try, fail, and try again.
		if cmd.NeedsDisplay && !includeGUI {
			continue
		}
		if len(only) > 0 && !only[cmd.Group] {
			continue
		}
		// `serve` itself is deliberately not exposed: an agent starting more
		// servers through the server is a loop nobody asked for.
		if cmd.Group == "serve" {
			continue
		}

		route := cmd.Route()
		tools = append(tools, toolnexus.NativeTool(
			toolName(route),
			describeTool(cmd),
			toolnexus.JSONSchema{
				"type": "object",
				"properties": map[string]any{
					"args": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Arguments, exactly as typed after `aos " + route + "`.",
					},
					"stdin": map[string]any{
						"type":        "string",
						"description": "Text piped to the command on stdin.",
					},
				},
			},
			func(ctx context.Context, args map[string]any) (string, error) {
				return invokeTool(c, route, args)
			},
		))
	}
	return tools
}

// describeTool writes the tool description an agent reads when choosing: the
// summary, the argument shape, and the examples the CLI already documents.
func describeTool(cmd *cli.Command) string {
	var b strings.Builder
	b.WriteString(cmd.Summary)
	if cmd.Args != "" {
		fmt.Fprintf(&b, "\n\nUsage: aos %s %s", cmd.Route(), cmd.Args)
	}
	if len(cmd.Examples) > 0 {
		b.WriteString("\n\nExamples:\n  " + strings.Join(cmd.Examples, "\n  "))
	}
	return b.String()
}

func invokeTool(c *cli.Ctx, route string, args map[string]any) (string, error) {
	argv := strings.Fields(route)
	if raw, ok := args["args"]; ok {
		extra, err := toStringSlice(raw)
		if err != nil {
			return "", err
		}
		argv = append(argv, extra...)
	}
	stdin, _ := args["stdin"].(string)

	result := cli.Invoke(c, argv, stdin)
	if result.Exit != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		if message == "" {
			message = fmt.Sprintf("exit status %d", result.Exit)
		}
		return "", fmt.Errorf("%s", message)
	}
	// Some commands say everything on stderr (progress, cursors); pass it along
	// rather than returning a confusingly empty success.
	output := result.Stdout
	if strings.TrimSpace(output) == "" {
		output = result.Stderr
	}
	if strings.TrimSpace(output) == "" {
		return "ok", nil
	}
	return output, nil
}

// toStringSlice accepts the array shapes a JSON tool call can produce.
func toStringSlice(raw any) ([]string, error) {
	switch value := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		return value, nil
	case string:
		if value == "" {
			return nil, nil
		}
		return []string{value}, nil
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("args must be strings, got %T", item)
			}
			out = append(out, text)
		}
		return out, nil
	}
	return nil, fmt.Errorf("args must be an array of strings, got %T", raw)
}

// guiMode decides whether the GUI commands are exposed as tools.
type guiMode int

const (
	// guiAuto exposes them only when this machine actually has a display.
	guiAuto guiMode = iota
	guiOn
	guiOff
)

func parseGUIMode(value string) (guiMode, error) {
	switch value {
	case "", "auto":
		return guiAuto, nil
	case "on", "yes", "true":
		return guiOn, nil
	case "off", "no", "false":
		return guiOff, nil
	}
	return guiAuto, fmt.Errorf("--gui must be on, off, or auto, got %q", value)
}

// groupFilter reads --groups=a,b into a set; an empty set means every group.
func groupFilter(set *argSet) map[string]bool {
	only := map[string]bool{}
	for _, name := range strings.Split(set.String("groups", ""), ",") {
		if name = strings.TrimSpace(name); name != "" {
			only[name] = true
		}
	}
	return only
}

func runServeTools(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "groups", "gui")
	if err != nil {
		return err
	}
	if err := set.Reject("groups", "gui", "json"); err != nil {
		return err
	}
	gui, err := parseGUIMode(set.String("gui", ""))
	if err != nil {
		return err
	}

	tools := buildTools(c, groupFilter(set), gui)
	if set.Has("json") {
		type toolJSON struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		payload := make([]toolJSON, 0, len(tools))
		for _, tool := range tools {
			payload = append(payload, toolJSON{tool.Name, tool.Description})
		}
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	for _, tool := range tools {
		summary, _, _ := strings.Cut(tool.Description, "\n")
		c.Printf("%-28s %s\n", tool.Name, summary)
	}
	c.Warnf("%d tools\n", len(tools))
	return nil
}

func runServeMCP(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "addr", "groups", "gui", "token", "socket")
	if err != nil {
		return err
	}
	if err := set.Reject("addr", "groups", "gui", "quiet", "token", "socket"); err != nil {
		return err
	}
	if socket := set.String("socket", ""); socket != "" {
		if set.Has("addr") || set.Has("token") {
			return fmt.Errorf("--socket is gated by the socket file's permissions; it takes no --addr or --token")
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return serveSocket(ctx, c, set, socket)
	}
	token, err := resolveServeToken(c, set)
	if err != nil {
		return err
	}
	gui, err := parseGUIMode(set.String("gui", ""))
	if err != nil {
		return err
	}

	addr := set.String("addr", defaultServeAddr)
	tools := buildTools(c, groupFilter(set), gui)
	if len(tools) == 0 {
		return fmt.Errorf("no commands matched --groups")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Builtins are toolnexus's own shell/file tools; aos already exposes
	// its own, so leaving them on would offer an agent two ways to do one thing.
	toolkit, err := toolnexus.CreateToolkit(ctx, toolnexus.Options{
		ExtraTools: tools,
		Builtins:   false,
	})
	if err != nil {
		return err
	}

	quiet := set.Has("quiet")
	// Bind the tool server to a private loopback port and put an authenticating
	// proxy on the address people actually connect to. Loopback alone is not a
	// permission: every process on the machine, and anything that can make a
	// browser issue a request, shares it. The MCP surface drives windows, input
	// and the filesystem, so it needs to be gated by something a caller has to
	// hold.
	handle, err := toolkit.Serve("127.0.0.1:0", toolnexus.ServeOptions{
		MCP: &toolnexus.MCPServeConfig{Name: "aos", Version: c.Version},
		OnCall: func(event toolnexus.OnCallEvent) {
			if quiet {
				return
			}
			status := "ok"
			if event.IsError {
				status = "error"
			}
			c.Warnf("%-28s %s %dms\n", event.Name, status, event.Ms)
		},
	})
	if err != nil {
		return err
	}
	defer handle.Stop()

	gate, err := serveGuarded(addr, handle.URL, token)
	if err != nil {
		return err
	}
	defer gate.Close()

	c.Printf("aos mcp  http://%s/mcp\n", gate.Addr().String())
	c.Printf("token           %s\n", token)
	c.Printf("tools           %d\n", len(tools))
	if !cli.HasDisplay(c.Env, c.GOOS) && gui != guiOn {
		c.Printf("gui             excluded — no display on this machine\n")
	}
	c.Printf("connect         claude mcp add --transport http aos http://%s/mcp \\\n", gate.Addr().String())
	c.Printf("                  --header \"Authorization: Bearer %s\"\n", token)

	<-ctx.Done()
	c.Warnf("\nstopping\n")
	return nil
}

// serveTokenEnv names the variable that pins the token across restarts, so a
// client configured once does not have to be reconfigured on every start.
const serveTokenEnv = "AOS_MCP_TOKEN"

// resolveServeToken picks the token: an explicit flag, then the environment,
// then a fresh random one for this run.
func resolveServeToken(c *cli.Ctx, set *argSet) (string, error) {
	if token := set.String("token", ""); token != "" {
		if len(token) < 16 {
			return "", fmt.Errorf("--token must be at least 16 characters; it is the only thing gating this machine")
		}
		return token, nil
	}
	if token := strings.TrimSpace(c.Env(serveTokenEnv)); token != "" {
		if len(token) < 16 {
			return "", fmt.Errorf("%s must be at least 16 characters", serveTokenEnv)
		}
		return token, nil
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// serveGuarded fronts the tool server with a listener that requires the token.
//
// The token may arrive as a bearer header, which is what an MCP client sends,
// or as ?t= for a client that cannot set headers at all.
func serveGuarded(addr, upstream, token string) (net.Listener, error) {
	target, err := url.Parse(upstream)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot bind %s: %w", addr, err)
	}

	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !tokenMatches(r, token) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		// The upstream never sees the credential; it is ours, not its.
		r.Header.Del("Authorization")
		proxy.ServeHTTP(w, r)
	})}
	go func() { _ = server.Serve(listener) }()
	return listener, nil
}

func tokenMatches(r *http.Request, token string) bool {
	if header := r.Header.Get("Authorization"); header != "" {
		if subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, "Bearer ")), []byte(token)) == 1 {
			return true
		}
	}
	return subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("t")), []byte(token)) == 1
}

// serveSocket serves MCP on a Unix socket, the host end of an SSH
// RemoteForward (ADR 0001). The socket is created 0600 in a 0700 folder: the
// file permission is the gate, so there is no token. Besides the per-command
// tools it offers aos_invoke, which another aos uses to delegate a screen
// command with its exit code and output files intact.
func serveSocket(ctx context.Context, c *cli.Ctx, set *argSet, socket string) error {
	gui, err := parseGUIMode(set.String("gui", ""))
	if err != nil {
		return err
	}
	if strings.HasPrefix(socket, "~/") {
		home, _ := os.UserHomeDir()
		socket = filepath.Join(home, socket[2:])
	}
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		return err
	}
	// A socket left by a server that died refuses connections; one that
	// answers belongs to a server still running.
	if conn, err := net.Dial("unix", socket); err == nil {
		conn.Close()
		return fmt.Errorf("an aos server is already listening on %s", socket)
	}
	_ = os.Remove(socket)

	// A remote caller gets screen commands only: per-command tools for the
	// routes that follow the host, plus aos_invoke, all running with a
	// ScreenOnly context so dispatch refuses anything else as well.
	screen := *c
	screen.ScreenOnly = true
	follows := map[string]bool{}
	for _, cmd := range c.Registry.Commands() {
		if cmd.FollowsHost {
			follows[toolName(cmd.Route())] = true
		}
	}
	var tools []toolnexus.Tool
	for _, tool := range buildTools(&screen, groupFilter(set), gui) {
		if follows[tool.Name] {
			tools = append(tools, tool)
		}
	}
	tools = append(tools, toolnexus.NativeTool(
		cli.SocketInvokeTool,
		"Run an aos screen command for another aos that delegated it over SSH. Returns JSON with exit, stdout, stderr and files.",
		toolnexus.JSONSchema{
			"type": "object",
			"properties": map[string]any{
				"argv":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"stdin":   map[string]any{"type": "string"},
				"outputs": map[string]any{"type": "object"},
				"from":    map[string]any{"type": "string"},
			},
			"required": []string{"argv"},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			raw, err := json.Marshal(args)
			if err != nil {
				return "", err
			}
			var req cli.SocketRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", err
			}
			out, err := json.Marshal(cli.ServeSocketInvoke(c, req))
			return string(out), err
		},
	))

	// The MCP handler is served on the socket itself. Nothing listens on a
	// loopback port, which any local process could reach without the socket.
	quiet := set.Has("quiet")
	mcpServer := mcpserver.NewMCPServer("aos", c.Version)
	for _, tool := range tools {
		tool := tool
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return err
		}
		mcpServer.AddTool(mcp.NewToolWithRawSchema(tool.Name, tool.Description, schema),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				started := time.Now()
				res, err := tool.Execute(req.GetArguments(), &toolnexus.ToolContext{Ctx: ctx})
				if err != nil {
					res = toolnexus.ToolResult{Output: err.Error(), IsError: true}
				}
				if !quiet {
					c.Warnf("%-28s %dms\n", tool.Name, time.Since(started).Milliseconds())
				}
				if res.IsError {
					return mcp.NewToolResultError(res.Output), nil
				}
				return mcp.NewToolResultText(res.Output), nil
			})
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpserver.NewStreamableHTTPServer(mcpServer))

	old := syscallUmask(0o077)
	listener, err := net.Listen("unix", socket)
	syscallUmask(old)
	if err != nil {
		return fmt.Errorf("cannot listen on %s: %w", socket, err)
	}
	defer os.Remove(socket)
	if err := os.Chmod(socket, 0o600); err != nil {
		listener.Close()
		return err
	}
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	c.Printf("aos mcp  unix://%s\n", socket)
	c.Printf("tools           %d\n", len(tools))
	c.Printf("ssh             add to ~/.ssh/config on this machine:\n")
	c.Printf("                  RemoteForward /tmp/aos-%%r.sock %s\n", socket)

	<-ctx.Done()
	c.Warnf("\nstopping\n")
	return nil
}
