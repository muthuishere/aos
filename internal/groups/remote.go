package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/windowctl"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("remote", "Hand this desktop's screen and input to a browser on the LAN")
		r.Add(
			&cli.Command{
				Group: "remote", Name: "share",
				Summary: "Share this screen and its mouse/keyboard over the LAN until interrupted",
				// Blocking is a security property here, not just a UX one. A
				// blocking command is excluded from the MCP tool list, so an
				// agent connected over MCP cannot quietly open a desktop-control
				// channel on someone's machine: a person has to start the share
				// at a terminal, watch it run, and Ctrl-C to revoke it. The link
				// lives exactly as long as the process a human is looking at.
				Blocking: true,
				// Sharing a screen needs a screen. Stamped here as well as via
				// guiGroups so the declaration is true on its own.
				NeedsDisplay: true,
				Args:         "[--monitor=<n>] [--port=<n>] [--fps=<n>] [--key=<k>]",
				Examples: []string{
					"aos remote share",
					"aos remote share --monitor=2 --fps=8",
				},
				Run: runRemoteShare,
			},
			&cli.Command{
				Group: "remote", Name: "url",
				Summary:      "Print the URL of the share running on this machine",
				NeedsDisplay: true,
				Examples:     []string{"aos remote url"},
				Run:          runRemoteURL,
			},
		)
	})
}

// remoteConfig is the optional <config-dir>/remote.json. Every field is a
// pointer so "absent" is distinguishable from "set to zero" — a config that
// pins `"port": 0` means "pick a free port", which is not the same as saying
// nothing at all.
//
// Defaults live here, in aos's own config dir, rather than in
// windowctl's environment: the two tools are configured separately on purpose,
// so a key exported for the windowctl CLI does not silently become the key for
// a share an aos user started.
type remoteConfig struct {
	Key     *string  `json:"key,omitempty"`
	Port    *int     `json:"port,omitempty"`
	FPS     *float64 `json:"fps,omitempty"`
	Monitor *int     `json:"monitor,omitempty"`
}

// remoteState records the running share so `remote url` can report it from a
// different process. Like headless.json it lives in the runtime config dir.
type remoteState struct {
	URL         string `json:"url"`
	LoopbackURL string `json:"loopback_url"`
	Port        int    `json:"port"`
	PID         int    `json:"pid"`
	StartedAt   string `json:"started_at"`
}

func remoteConfigPath(env func(string) string) string {
	return filepath.Join(cli.ConfigDir(env), "remote.json")
}

func remoteStatePath(env func(string) string) string {
	return filepath.Join(cli.ConfigDir(env), "remote-state.json")
}

// loadRemoteConfig reads remote.json. A missing file is not an error — the
// config is entirely optional — but a malformed one is, because silently
// falling back to a generated key when the user believes they pinned a stable
// one would change who can reach the machine.
func loadRemoteConfig(path string) (remoteConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return remoteConfig{}, nil
		}
		return remoteConfig{}, err
	}
	var cfg remoteConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return remoteConfig{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// remoteDefaults are the built-in values, the bottom of the precedence stack.
const (
	remoteDefaultFPS = 10.0
)

// resolveRemoteOptions folds flags, the config file, the environment, and the
// built-in defaults into one windowctl.RemoteOptions. Precedence is
// flags > config file > built-in, with AOS_REMOTE_KEY sitting between
// the flag and the file for the key alone: an env var is how a launcher or a
// service unit injects a secret, so it must beat a file on disk, while an
// explicit --key typed at the terminal still wins over everything.
//
// generated reports that no key was configured anywhere and windowctl will mint
// a fresh one, which the caller must say out loud — a per-run key means
// yesterday's link is dead, and someone expecting a stable link should learn
// that from the output rather than from a broken bookmark.
func resolveRemoteOptions(set *argSet, cfg remoteConfig, env func(string) string) (opts windowctl.RemoteOptions, generated bool, err error) {
	// --- key ---
	key := set.String("key", "")
	if key == "" {
		key = env("AOS_REMOTE_KEY")
	}
	if key == "" && cfg.Key != nil {
		key = *cfg.Key
	}
	// Validate here as well as in the library so the message names the place
	// the value came from rather than talking about a Go field.
	if key != "" && len(key) < 16 {
		return opts, false, fmt.Errorf("remote key must be at least 16 characters — it is the only thing gating control of this desktop (got %d)", len(key))
	}
	generated = key == ""

	// --- port ---
	port := 0
	if cfg.Port != nil {
		port = *cfg.Port
	}
	port, err = set.Int("port", port)
	if err != nil {
		return opts, false, err
	}
	if port < 0 || port > 65535 {
		return opts, false, fmt.Errorf("--port must be between 0 and 65535, got %d", port)
	}

	// --- fps ---
	fps := remoteDefaultFPS
	if cfg.FPS != nil {
		fps = *cfg.FPS
	}
	if raw := set.String("fps", ""); raw != "" {
		fps, err = strconv.ParseFloat(raw, 64)
		if err != nil {
			return opts, false, fmt.Errorf("--fps must be a number, got %q", raw)
		}
	}
	if fps <= 0 {
		return opts, false, fmt.Errorf("--fps must be greater than 0, got %v", fps)
	}

	// --- monitor ---
	// nil means "the focused one", which is why this stays a pointer all the
	// way down instead of collapsing to 0.
	monitor := cfg.Monitor
	if set.Has("monitor") {
		monitor, err = set.IntPtr("monitor")
		if err != nil {
			return opts, false, err
		}
	}
	if monitor != nil && *monitor < 1 {
		return opts, false, fmt.Errorf("--monitor must be 1 or higher (omit it to share the focused screen)")
	}

	return windowctl.RemoteOptions{
		Monitor: monitor,
		Port:    port,
		FPS:     fps,
		Key:     key,
		Title:   "aos remote",
	}, generated, nil
}

func runRemoteShare(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "monitor", "port", "fps", "key")
	if err != nil {
		return err
	}
	if err := set.Reject("monitor", "port", "fps", "key"); err != nil {
		return err
	}

	cfg, err := loadRemoteConfig(remoteConfigPath(c.Env))
	if err != nil {
		return err
	}
	opts, generated, err := resolveRemoteOptions(set, cfg, c.Env)
	if err != nil {
		return err
	}

	handle, err := windowctl.Remote(opts)
	if err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	defer handle.Stop()

	// Record the share before announcing it, so `remote url` in another
	// terminal can answer the moment the link is printed here.
	state := remoteState{
		URL:         handle.URL,
		LoopbackURL: handle.LoopbackURL,
		Port:        handle.Port,
		PID:         os.Getpid(),
		StartedAt:   time.Now().Format(time.RFC3339),
	}
	if err := writeRemoteState(c.Env, state); err != nil {
		c.Warnf("aos: could not record the share for `remote url`: %v\n", err)
	}
	defer clearRemoteState(c.Env)

	c.Println("Screen share and CONTROL are live on this machine.")
	c.Println("The link carries the access key, so the link IS the key — share it like one.")
	c.Printf("\n  LAN       %s\n", handle.URL)
	c.Printf("  Local     %s\n\n", handle.LoopbackURL)
	if generated {
		c.Println("This run minted a fresh key, so this link dies with this process.")
		c.Printf("For a link that survives a restart, set a key in %s or AOS_REMOTE_KEY.\n\n", remoteConfigPath(c.Env))
	}
	c.Println("Press Ctrl-C to revoke the link and stop.")

	// Ctrl-C is the revoke button, so the share must outlive nothing: the
	// deferred Stop tears the server down the moment the signal lands.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	<-ctx.Done()

	c.Println("\naos: link revoked.")
	return nil
}

func runRemoteURL(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`remote url` takes no arguments")
	}
	state, live := readRemoteState(c.Env)
	if !live {
		return &cli.ExitError{Code: 1, Message: "no share is running; start one with `aos remote share`"}
	}
	c.Println(state.URL)
	c.Printf("%s\n", state.LoopbackURL)
	return nil
}

func writeRemoteState(env func(string) string, state remoteState) error {
	path := remoteStatePath(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	// 0o600: the file holds the URL, and the URL holds the access key.
	return os.WriteFile(path, data, 0o600)
}

func clearRemoteState(env func(string) string) {
	if err := os.Remove(remoteStatePath(env)); err != nil && !os.IsNotExist(err) {
		// Nothing useful to do about it, and the liveness probe below means a
		// leftover file cannot pass itself off as a running share.
		_ = err
	}
}

// readRemoteState returns the recorded share and whether it is actually live.
// Liveness is a TCP connect to the recorded port rather than a PID check: a PID
// can be recycled, and what a caller of `remote url` needs to know is whether
// the URL they are about to open still answers.
func readRemoteState(env func(string) string) (remoteState, bool) {
	data, err := os.ReadFile(remoteStatePath(env))
	if err != nil {
		return remoteState{}, false
	}
	var state remoteState
	if err := json.Unmarshal(data, &state); err != nil {
		return remoteState{}, false
	}
	if state.Port == 0 {
		return state, false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(state.Port)), 500*time.Millisecond)
	if err != nil {
		return state, false
	}
	_ = conn.Close()
	return state, true
}
