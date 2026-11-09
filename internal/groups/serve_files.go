package groups

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "serve", Name: "files",
			Summary:  "Serve a directory read-only over HTTP, with listings",
			Args:     "<dir> [--lan] [--port=<n>] [--for=<dur>] [--hidden] [--json]",
			Blocking: true,
			Examples: []string{
				"aos serve files ./dist",
				"aos serve files ~/Downloads --lan --for=10m   # phone on the same wifi; URL carries a token",
				"aos serve files . --port=8000 --json   # one JSON line per request",
				"aos service create share --now -- aos serve files ~/share --lan --port=8080   # keep it alive",
			},
			Run: runServeFiles,
		})
	})
}

// fileServerOptions is what the handler needs; the command fills it from flags
// and tests build it directly.
type fileServerOptions struct {
	Root   *os.Root
	Token  string // empty: no path token (loopback)
	Hidden bool
	Log    func(serveFilesEvent)
}

type serveFilesEvent struct {
	Time   string `json:"time"`
	Remote string `json:"remote"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	Bytes  int64  `json:"bytes"`
	Ms     int64  `json:"ms"`
}

func runServeFiles(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "port", "for")
	if err != nil {
		return err
	}
	if err := set.Reject("lan", "port", "for", "hidden", "json"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("usage: aos serve files <dir> [--lan] [--port=<n>] [--for=<dur>] [--hidden] [--json]")
	}
	dir := expandHome(set.Rest[0])
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	port, err := set.Int("port", 0)
	if err != nil {
		return err
	}
	if port < 0 || port > 65535 {
		return fmt.Errorf("--port must be 0-65535, got %d", port)
	}
	var dur time.Duration
	if v := set.String("for", ""); v != "" {
		dur, err = time.ParseDuration(v)
		if err != nil || dur <= 0 {
			return fmt.Errorf("--for wants a duration like 30s, 10m or 1h, got %q", v)
		}
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()

	host := "127.0.0.1"
	opts := fileServerOptions{Root: root, Hidden: set.Has("hidden")}
	if set.Has("lan") {
		host = serveLANIP()
		buf := make([]byte, 12)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		opts.Token = hex.EncodeToString(buf)
	}
	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		opts.Log = func(e serveFilesEvent) { _ = enc.Encode(e) }
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	url := "http://" + ln.Addr().String() + "/"
	if opts.Token != "" {
		url += opts.Token + "/"
	}
	if set.Has("json") {
		_ = json.NewEncoder(c.Stdout).Encode(map[string]any{"url": url, "dir": dir})
	} else {
		c.Printf("serving  %s\n", dir)
		c.Printf("url      %s\n", url)
		if dur > 0 {
			c.Printf("stops    in %s\n", dur)
		}
	}

	srv := &http.Server{Handler: newFileServer(opts), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if dur > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, dur)
		defer cancel()
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shut, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shut)
	return nil
}

// serveLANIP picks the address other devices on the network reach: the local
// end of a UDP "connection" toward a public address (no packet is sent).
func serveLANIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

// newFileServer returns the read-only handler. os.Root refuses any path or
// symlink that resolves outside the directory; the filter on top hides
// dotfiles (unless Hidden) and escaping symlinks from listings too.
func newFileServer(o fileServerOptions) http.Handler {
	files := http.FileServer(http.FS(filteredFS{root: o.Root, hidden: o.Hidden}))
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		files.ServeHTTP(w, r)
	})
	if o.Token != "" {
		prefix := "/" + o.Token
		inner := h
		h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == prefix {
				http.Redirect(w, r, prefix+"/", http.StatusMovedPermanently)
				return
			}
			if !strings.HasPrefix(r.URL.Path, prefix+"/") {
				http.NotFound(w, r)
				return
			}
			http.StripPrefix(prefix, inner).ServeHTTP(w, r)
		})
	}
	if o.Log == nil {
		return h
	}
	inner := h
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		inner.ServeHTTP(rec, r)
		p := r.URL.Path
		if o.Token != "" {
			p = strings.Replace(p, o.Token, "<token>", 1)
		}
		o.Log(serveFilesEvent{
			Time: start.UTC().Format(time.RFC3339), Remote: r.RemoteAddr, Method: r.Method,
			Path: p, Status: rec.status, Bytes: rec.n, Ms: time.Since(start).Milliseconds(),
		})
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	n      int64
}

func (s *statusRecorder) WriteHeader(code int) { s.status = code; s.ResponseWriter.WriteHeader(code) }
func (s *statusRecorder) Write(b []byte) (int, error) {
	n, err := s.ResponseWriter.Write(b)
	s.n += int64(n)
	return n, err
}

// filteredFS is the served view of the root.
type filteredFS struct {
	root   *os.Root
	hidden bool
}

func (f filteredFS) visible(name string) bool {
	if f.hidden || name == "." {
		return true
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

func (f filteredFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) || !f.visible(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	file, err := f.root.FS().Open(name)
	if err != nil {
		// Escaping symlinks surface as a distinct error; report them as absent.
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if info, serr := file.Stat(); serr == nil && info.IsDir() {
		if dir, ok := file.(fs.ReadDirFile); ok {
			return &filteredDir{ReadDirFile: dir, fs: f, dir: name}, nil
		}
	}
	return file, nil
}

type filteredDir struct {
	fs.ReadDirFile
	fs  filteredFS
	dir string
}

func (d *filteredDir) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := d.ReadDirFile.ReadDir(n)
	out := entries[:0]
	for _, e := range entries {
		full := path.Join(d.dir, e.Name())
		if !d.fs.visible(full) {
			continue
		}
		if e.Type()&fs.ModeSymlink != 0 {
			if _, serr := d.fs.root.Stat(full); serr != nil {
				continue // points outside the root, or nowhere
			}
		}
		out = append(out, e)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return out, err
	}
	if n > 0 && len(out) == 0 && err == nil {
		// Everything in this batch was filtered; read on.
		return d.ReadDir(n)
	}
	return out, err
}
