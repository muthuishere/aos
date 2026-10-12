package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Detached drawings: a CLI re-runs itself as a detached child that shows
// the drawing until it ends or is stopped. Live drawings are recorded in
// <UserCacheDir>/windowctl/draw/<pid>.json so `draw stop` can end them
// and new drawings keep their signs clear of live ones. The registry is
// shared by every binary built on this package (windowctl, aos).

// DrawChildEnv is set to "1" in the environment of a detached drawing.
const DrawChildEnv = "AOS_DRAW_CHILD"

// DrawTagEnv carries a detached drawing's tag (e.g. "subtitle") from
// SpawnDetachedDrawTagged to RegisterDrawing.
const DrawTagEnv = "AOS_DRAW_TAG"

type drawEntry struct {
	PID    int        `json:"PID"`
	Result DrawResult `json:"Result"`
	Tag    string     `json:"Tag,omitempty"`
}

func drawRegistryDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "aos", "draw")
}

func drawEntryPath(pid int) string {
	return filepath.Join(drawRegistryDir(), strconv.Itoa(pid)+".json")
}

// drawStopPath is the marker `draw stop` writes on Windows to ask a
// drawing to fade out and exit (there is no SIGTERM there).
func drawStopPath(pid int) string {
	return filepath.Join(drawRegistryDir(), strconv.Itoa(pid)+".stop")
}

// DrawStopRequested reports whether `draw stop` asked this process (a
// detached drawing) to end; the child polls it to fade out cleanly.
func DrawStopRequested() bool {
	_, err := os.Stat(drawStopPath(os.Getpid()))
	return err == nil
}

// IsDrawChild reports whether this process is a detached drawing.
func IsDrawChild() bool { return os.Getenv(DrawChildEnv) == "1" }

// RegisterDrawing records this process as a live drawing; the parent
// waiting in SpawnDetachedDraw returns as soon as it appears.
func RegisterDrawing(res DrawResult) {
	_ = os.MkdirAll(drawRegistryDir(), 0o755)
	b, _ := json.Marshal(drawEntry{PID: os.Getpid(), Result: res, Tag: os.Getenv(DrawTagEnv)})
	tmp := drawEntryPath(os.Getpid()) + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, drawEntryPath(os.Getpid()))
	}
}

// UnregisterDrawing removes this process from the registry.
func UnregisterDrawing() { _ = os.Remove(drawEntryPath(os.Getpid())) }

func liveDrawings() []drawEntry {
	files, _ := filepath.Glob(filepath.Join(drawRegistryDir(), "*.json"))
	var out []drawEntry
	for _, f := range files {
		b, err := os.ReadFile(f)
		var e drawEntry
		if err != nil || json.Unmarshal(b, &e) != nil || !processAlive(e.PID) {
			_ = os.Remove(f)
			continue
		}
		out = append(out, e)
	}
	return out
}

// LiveDrawingSigns returns the sign rects of every live drawing, for
// DrawOptions.Avoid.
func LiveDrawingSigns() []Rect {
	var out []Rect
	for _, e := range liveDrawings() {
		out = append(out, e.Result.Sign)
	}
	return out
}

// StopDrawings ends every live drawing and returns how many it stopped.
func StopDrawings() int { return StopDrawingsTagged("") }

// StopDrawingsTagged ends the live drawings with this tag ("" = all).
func StopDrawingsTagged(tag string) int {
	n := 0
	for _, e := range liveDrawings() {
		if tag != "" && e.Tag != tag {
			continue
		}
		if stopProcess(e.PID) == nil {
			n++
		}
		_ = os.Remove(drawEntryPath(e.PID))
	}
	return n
}

// SpawnDetachedDraw re-runs the current executable with args (the full
// sub-command line, e.g. ["draw", "--at", "10,10", "--text", "hi"], with
// any detach flag already removed) as a detached child, and waits until
// it registers so the caller learns where the drawing landed — or why it
// failed — before returning. The child must call RegisterDrawing.
func SpawnDetachedDraw(args []string) (DrawResult, error) {
	return SpawnDetachedDrawTagged(args, "")
}

// SpawnDetachedDrawTagged is SpawnDetachedDraw with a tag recorded in the
// registry, so StopDrawingsTagged can end just that kind of drawing.
func SpawnDetachedDrawTagged(args []string, tag string) (DrawResult, error) {
	exe, err := os.Executable()
	if err != nil {
		return DrawResult{}, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), DrawChildEnv+"=1", DrawTagEnv+"="+tag)
	errPath := filepath.Join(os.TempDir(), fmt.Sprintf("aos-draw-%d.err", time.Now().UnixNano()))
	errFile, err := os.Create(errPath)
	if err != nil {
		return DrawResult{}, err
	}
	defer os.Remove(errPath)
	cmd.Stderr = errFile
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		errFile.Close()
		return DrawResult{}, err
	}
	errFile.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(10 * time.Second)
	for {
		if b, err := os.ReadFile(drawEntryPath(cmd.Process.Pid)); err == nil {
			var e drawEntry
			if json.Unmarshal(b, &e) == nil {
				return e.Result, nil
			}
		}
		select {
		case <-exited:
			msg, _ := os.ReadFile(errPath)
			return DrawResult{}, fmt.Errorf("drawing failed: %s", strings.TrimSpace(string(msg)))
		case <-deadline:
			return DrawResult{}, errors.New("drawing did not start within 10s")
		case <-time.After(50 * time.Millisecond):
		}
	}
}
