// remote.go — the LAN screen-share-and-control server, exposed as a
// library API (Remote / RemoteHandle / RemoteToken).
//
// This is a thin transport layer over the public automation API
// (Screenshot / MouseClick / TypeText / PressKey): an HTTP server
// streams the screen as live MJPEG (multipart/x-mixed-replace, JPEG
// frames pushed continuously — far smoother than single-frame polling)
// and forwards clicks/keys/touches back. It puts no new
// window-management logic in the transport — the library still owns
// every primitive.
//
// Security model (deliberate, because this grants desktop control to
// whoever opens the URL): the server mints a fresh random token per run
// (unless the caller supplies one) and rejects every request without
// it. The token rides in the URL we return, so the link IS the key —
// share it like a house key and Stop() the server to revoke. By default
// it binds all interfaces so LAN devices (a phone, another laptop) can
// reach it directly; the token, not the bind address, is what gates
// access.
//
// Deliberate limit: this API is LAN-only and has NO ability to open a
// public tunnel — there is no cloudflared here and no hook to add one.
// A library that could publish a desktop-control endpoint to the open
// internet from inside a caller's process is exactly the capability we
// do not want to hand out. A program that genuinely wants a public URL
// must start its own tunnel, itself, against the Port this returns —
// which is precisely what `windowctl remote --tunnel` does, in the CLI.
package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"os"
	"strconv"
	"sync"
	"time"
)

// RemoteOptions configures a LAN screen-share-and-control server.
type RemoteOptions struct {
	Monitor *int    // default monitor to share; nil means the focused one
	Port    int     // 0 picks a free port
	FPS     float64 // clamped to 1..30; 0 means 10
	Quality int     // JPEG quality; 0 means 60
	Key     string  // access token; must be >= 16 chars. Empty generates one.
	Bind    string  // listen address; empty means all interfaces
	Title   string  // viewer page title; empty means "aos remote"
}

// RemoteHandle is a running server.
type RemoteHandle struct {
	URL         string // LAN URL including the access token
	LoopbackURL string // 127.0.0.1 URL including the access token
	Port        int
	Key         string

	httpSrv  *http.Server
	stopOnce sync.Once
	stopErr  error
}

// Remote starts the server and returns once it is listening; it does
// NOT block. Every failure comes back as an error — this never prints
// and never exits, because the caller (a CLI, an agent runtime, a test)
// owns its own output and signal handling. Call Stop to revoke access.
func Remote(opts RemoteOptions) (*RemoteHandle, error) {
	// A short key is a footgun on a control channel that grants full
	// desktop input, so demand real entropy instead of quietly
	// accepting something guessable.
	key := opts.Key
	if key != "" && len(key) < 16 {
		return nil, errors.New("remote: key must be at least 16 characters (it is the only thing gating desktop control)")
	}
	if key == "" {
		key = RemoteToken()
		if key == "" {
			return nil, errors.New("remote: cannot generate session token")
		}
	}

	fps := opts.FPS
	if fps == 0 {
		fps = 10
	}
	if fps < 1 {
		fps = 1
	}
	if fps > 30 {
		fps = 30
	}
	quality := opts.Quality
	if quality == 0 {
		quality = 60
	}
	if opts.Monitor != nil && *opts.Monitor < 1 {
		return nil, errors.New("remote: Monitor must be >= 1 (use 1, 2, 3, ...; nil to auto-resolve)")
	}

	title := opts.Title
	if title == "" {
		title = "aos remote"
	}
	srv := &remoteServer{
		title:          title,
		defaultMonitor: opts.Monitor,
		token:          key,
		frameInterval:  time.Duration(float64(time.Second) / fps),
		quality:        quality,
	}

	// Bind all interfaces by default so other devices on the LAN can
	// reach the viewer (a phone, another laptop) — the token gate is
	// what protects this, not the bind address.
	ln, err := net.Listen("tcp", opts.Bind+":"+strconv.Itoa(opts.Port))
	if err != nil {
		return nil, fmt.Errorf("remote: cannot bind port: %w", err)
	}
	actualPort := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/", srv.auth(srv.handleIndex))
	mux.HandleFunc("/stream", srv.auth(srv.handleStream))
	mux.HandleFunc("/frame", srv.auth(srv.handleFrame))
	mux.HandleFunc("/monitors", srv.auth(srv.handleMonitors))
	mux.HandleFunc("/input", srv.auth(srv.handleInput))
	httpSrv := &http.Server{Handler: mux}

	go func() {
		// Serve owns the listener from here. There is nobody to report
		// to — Remote has already returned and a library must not write
		// to the caller's stderr — and a dead listener surfaces to
		// viewers as a refused connection anyway.
		_ = httpSrv.Serve(ln)
	}()

	return &RemoteHandle{
		URL:         fmt.Sprintf("http://%s:%d/?t=%s", lanIP(), actualPort, key),
		LoopbackURL: fmt.Sprintf("http://127.0.0.1:%d/?t=%s", actualPort, key),
		Port:        actualPort,
		Key:         key,
		httpSrv:     httpSrv,
	}, nil
}

// Stop shuts the server down, revoking the link. It is idempotent: a
// repeat call returns the first result rather than erroring, so a
// defer and an explicit stop can safely coexist.
func (h *RemoteHandle) Stop() error {
	if h == nil || h.httpSrv == nil {
		return nil
	}
	h.stopOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		h.stopErr = h.httpSrv.Shutdown(ctx)
	})
	return h.stopErr
}

// RemoteToken mints a fresh random access token. It returns "" when
// crypto/rand fails, which callers must treat as fatal: degrading to a
// predictable token on a desktop-control channel is not an option.
func RemoteToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

type remoteServer struct {
	title          string
	defaultMonitor *int
	token          string
	frameInterval  time.Duration
	quality        int

	// screenshotMu serializes captures — the darwin capture path and
	// its temp file are not concurrency-safe. With the shared feeds
	// below only one capture runs per monitor; the lock still guards
	// concurrent feeds for different monitors and the /frame fallback.
	screenshotMu sync.Mutex

	// feeds holds one shared capture loop per monitor, keyed by the
	// monitor query value ("" = default/focused). Every viewer of a
	// given monitor subscribes to the SAME feed rather than starting
	// its own capture — so N tabs / devices / reconnects of one monitor
	// never spin up N contending capture loops (which would thrash the
	// screenshot lock and cut everyone's framerate).
	feedsMu sync.Mutex
	feeds   map[string]*monitorFeed
}

// monitorFeed is a single shared capture loop for one monitor. Its
// latest JPEG frame is broadcast to all subscribers via cond. It is
// reference-counted: the loop starts on the first subscriber and stops
// when the last one leaves.
type monitorFeed struct {
	key       string
	monitorID *int
	srv       *remoteServer

	mu      sync.Mutex
	cond    *sync.Cond
	latest  []byte
	rect    Rect
	seq     uint64 // bumped per new frame; 0 = none captured yet
	refs    int
	stopped bool
	err     error
}

// acquireFeed returns the shared feed for a monitor key, starting its
// capture loop on first use, and increments the refcount.
func (s *remoteServer) acquireFeed(key string, monitorID *int) *monitorFeed {
	s.feedsMu.Lock()
	defer s.feedsMu.Unlock()
	if s.feeds == nil {
		s.feeds = map[string]*monitorFeed{}
	}
	f, ok := s.feeds[key]
	if !ok {
		f = &monitorFeed{key: key, monitorID: monitorID, srv: s}
		f.cond = sync.NewCond(&f.mu)
		s.feeds[key] = f
		go f.captureLoop()
	}
	f.refs++
	return f
}

// releaseFeed drops a subscriber; when the last leaves it stops the
// capture loop (and wakes any straggler waiter so none block forever).
func (s *remoteServer) releaseFeed(f *monitorFeed) {
	s.feedsMu.Lock()
	last := false
	f.refs--
	if f.refs <= 0 {
		delete(s.feeds, f.key)
		last = true
	}
	s.feedsMu.Unlock()
	if last {
		f.mu.Lock()
		f.stopped = true
		f.cond.Broadcast()
		f.mu.Unlock()
	}
}

// captureLoop captures the monitor at the server frame interval and
// broadcasts each new frame. It exits once the feed is marked stopped.
func (f *monitorFeed) captureLoop() {
	tmp, err := os.CreateTemp("", "aos-feed-*.png")
	if err != nil {
		f.mu.Lock()
		f.err = err
		f.cond.Broadcast()
		f.mu.Unlock()
		return
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	var buf bytes.Buffer
	ticker := time.NewTicker(f.srv.frameInterval)
	defer ticker.Stop()
	for {
		rect, cerr := f.srv.captureJPEG(f.monitorID, tmpPath, f.srv.quality, &buf)
		f.mu.Lock()
		if f.stopped {
			f.mu.Unlock()
			return
		}
		if cerr != nil {
			f.err = cerr
		} else {
			f.err = nil
			f.latest = append(f.latest[:0], buf.Bytes()...)
			f.rect = rect
			f.seq++
		}
		f.cond.Broadcast()
		f.mu.Unlock()
		<-ticker.C
	}
}

// waitNext blocks until a frame newer than lastSeq exists (or the feed
// stops / errors), returning a copy of the frame, its rect, and the new
// seq. A stopped feed returns http.ErrServerClosed.
func (f *monitorFeed) waitNext(lastSeq uint64) ([]byte, Rect, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.seq == lastSeq && f.err == nil && !f.stopped {
		f.cond.Wait()
	}
	if f.stopped {
		return nil, Rect{}, lastSeq, http.ErrServerClosed
	}
	if f.err != nil && f.seq == lastSeq {
		return nil, Rect{}, lastSeq, f.err
	}
	frame := make([]byte, len(f.latest))
	copy(frame, f.latest)
	return frame, f.rect, f.seq, nil
}

// lanIP returns the machine's primary LAN IPv4 by opening a UDP socket
// toward a public address and reading the local address the OS picked
// (no packets are sent). Falls back to 127.0.0.1 when offline.
func lanIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "127.0.0.1"
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.String()
}

func (s *remoteServer) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.URL.Query().Get("t")
		if got == "" {
			got = r.Header.Get("X-Windowctl-Token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *remoteServer) handleMonitors(w http.ResponseWriter, r *http.Request) {
	ms, err := ListMonitors()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ms)
}

func (s *remoteServer) monitorFromQuery(r *http.Request) *int {
	monitorID := s.defaultMonitor
	if q := r.URL.Query().Get("monitor"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n >= 1 {
			monitorID = &n
		}
	}
	return monitorID
}

// captureJPEG captures the monitor to a JPEG (via the PNG the adapter
// writes, decoded and re-encoded). tmpPath is reused across a stream's
// frames so we don't churn temp files. quality is the JPEG quality
// (1-100). Returns the encoded bytes and the captured rect.
func (s *remoteServer) captureJPEG(monitorID *int, tmpPath string, quality int, buf *bytes.Buffer) (Rect, error) {
	s.screenshotMu.Lock()
	rect, err := Screenshot(ScreenshotOptions{Monitor: monitorID, OutPath: tmpPath})
	s.screenshotMu.Unlock()
	if err != nil {
		return Rect{}, err
	}
	raw, err := os.ReadFile(tmpPath)
	if err != nil {
		return Rect{}, err
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return Rect{}, err
	}
	buf.Reset()
	if err := jpeg.Encode(buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return Rect{}, err
	}
	return rect, nil
}

// handleStream is the live path: an MJPEG (multipart/x-mixed-replace)
// stream the browser renders natively in an <img>. It subscribes to the
// SHARED per-monitor feed (acquireFeed) instead of capturing on its own,
// so any number of viewers of one monitor read a single capture loop —
// each frame captured once and fanned out. Geometry rides in per-part
// headers. The viewer also reads geometry from /monitors as a fallback.
func (s *remoteServer) handleStream(w http.ResponseWriter, r *http.Request) {
	monitorID := s.monitorFromQuery(r)
	key := r.URL.Query().Get("monitor") // "" == default/focused

	feed := s.acquireFeed(key, monitorID)
	defer s.releaseFeed(feed)

	mw := multipart.NewWriter(w)
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary="+mw.Boundary())
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)
	ctx := r.Context()
	var lastSeq uint64

	for {
		if ctx.Err() != nil {
			return
		}
		frame, rect, seq, err := feed.waitNext(lastSeq)
		if err != nil {
			return
		}
		lastSeq = seq
		hdr := make(textproto.MIMEHeader)
		hdr.Set("Content-Type", "image/jpeg")
		hdr.Set("Content-Length", strconv.Itoa(len(frame)))
		hdr.Set("X-Origin-X", strconv.Itoa(rect.X))
		hdr.Set("X-Origin-Y", strconv.Itoa(rect.Y))
		hdr.Set("X-Width", strconv.Itoa(rect.W))
		hdr.Set("X-Height", strconv.Itoa(rect.H))
		part, err := mw.CreatePart(hdr)
		if err != nil {
			return
		}
		if _, err := part.Write(frame); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// handleFrame captures the requested monitor and streams the PNG. The
// captured rect's global origin rides in headers so the viewer can map
// an image-pixel click back to a global coordinate (screenshots are
// point-normalized: origin + pixel == global point). Kept as a
// single-frame fallback for clients that can't render MJPEG.
func (s *remoteServer) handleFrame(w http.ResponseWriter, r *http.Request) {
	monitorID := s.monitorFromQuery(r)

	tmp, err := os.CreateTemp("", "aos-remote-*.png")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	s.screenshotMu.Lock()
	rect, err := Screenshot(ScreenshotOptions{Monitor: monitorID, OutPath: tmpPath})
	s.screenshotMu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := os.ReadFile(tmpPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Origin-X", strconv.Itoa(rect.X))
	w.Header().Set("X-Origin-Y", strconv.Itoa(rect.Y))
	w.Header().Set("X-Width", strconv.Itoa(rect.W))
	w.Header().Set("X-Height", strconv.Itoa(rect.H))
	_, _ = w.Write(data)
}

// inputAction is the wire shape the viewer POSTs. X/Y are ALWAYS
// absolute global coordinates (the viewer already added the frame
// origin), so the server passes them through with no monitor context.
type inputAction struct {
	Type   string `json:"type"` // "click" | "move" | "key" | "text"
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Button string `json:"button"` // "left" | "right" | "middle"
	Double bool   `json:"double"`
	Combo  string `json:"combo"` // for type "key"
	Text   string `json:"text"`  // for type "text"
}

func (s *remoteServer) handleInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var a inputAction
	if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	var err error
	switch a.Type {
	case "move":
		err = MouseMove(nil, a.X, a.Y)
	case "click":
		x, y := a.X, a.Y
		opts := ClickOptions{X: &x, Y: &y, Double: a.Double}
		switch a.Button {
		case "right":
			opts.Button = MouseRight
		case "middle":
			opts.Button = MouseMiddle
		}
		err = MouseClick(opts)
	case "key":
		err = PressKey(a.Combo)
	case "text":
		err = TypeText(a.Text)
	default:
		http.Error(w, "unknown action type "+a.Type, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *remoteServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	buf.WriteString(remoteViewerHTML(s.token, int(s.frameInterval/time.Millisecond), s.title))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}
