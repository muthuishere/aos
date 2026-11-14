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
	PID    int          `json:"PID"`
	Result DrawResult   `json:"Result"`
	Tag    string       `json:"Tag,omitempty"`
	Cover  *CoverResult `json:"Cover,omitempty"`
}

// CoverTag is the registry tag of a detached cover.
const CoverTag = "cover"

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
		if e.Cover == nil {
			out = append(out, e.Result.Sign)
		}
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
	e, err := spawnDetached(args, tag)
	return e.Result, err
}

// SpawnDetachedCover is SpawnDetachedDraw for `draw cover`: the child
// calls RegisterCover, and its first placement comes back.
func SpawnDetachedCover(args []string) (CoverResult, int, error) {
	e, err := spawnDetached(args, CoverTag)
	if err != nil {
		return CoverResult{}, 0, err
	}
	if e.Cover == nil {
		return CoverResult{}, e.PID, errors.New("cover: the drawing registered without a cover")
	}
	return *e.Cover, e.PID, nil
}

func spawnDetached(args []string, tag string) (drawEntry, error) {
	exe, err := os.Executable()
	if err != nil {
		return drawEntry{}, err
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), DrawChildEnv+"=1", DrawTagEnv+"="+tag)
	errPath := filepath.Join(os.TempDir(), fmt.Sprintf("aos-draw-%d.err", time.Now().UnixNano()))
	errFile, err := os.Create(errPath)
	if err != nil {
		return drawEntry{}, err
	}
	defer os.Remove(errPath)
	cmd.Stderr = errFile
	detachProcess(cmd)
	if err := cmd.Start(); err != nil {
		errFile.Close()
		return drawEntry{}, err
	}
	errFile.Close()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	deadline := time.After(10 * time.Second)
	for {
		if b, err := os.ReadFile(drawEntryPath(cmd.Process.Pid)); err == nil {
			var e drawEntry
			if json.Unmarshal(b, &e) == nil {
				return e, nil
			}
		}
		select {
		case <-exited:
			msg, _ := os.ReadFile(errPath)
			return drawEntry{}, fmt.Errorf("drawing failed: %s", strings.TrimSpace(string(msg)))
		case <-deadline:
			return drawEntry{}, errors.New("drawing did not start within 10s")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// RegisterCover records this process (a detached cover) and where its
// cover is now; it is called again whenever the cover moves.
func RegisterCover(res CoverResult) {
	_ = os.MkdirAll(drawRegistryDir(), 0o755)
	b, _ := json.Marshal(drawEntry{PID: os.Getpid(), Tag: CoverTag, Cover: &res})
	tmp := drawEntryPath(os.Getpid()) + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, drawEntryPath(os.Getpid()))
	}
}

// LiveCover is a detached cover on screen.
type LiveCover struct {
	PID   int         `json:"PID"`
	Cover CoverResult `json:"Cover"`
}

// LiveCovers lists every detached cover.
func LiveCovers() []LiveCover {
	var out []LiveCover
	for _, e := range liveDrawings() {
		if e.Cover != nil {
			out = append(out, LiveCover{PID: e.PID, Cover: *e.Cover})
		}
	}
	return out
}

// StopCovers ends the detached covers with this id ("" = every cover),
// except the process skip (0 = none), and returns how many it stopped.
func StopCovers(id string, skip int) int {
	n := 0
	for _, c := range LiveCovers() {
		if (id != "" && c.Cover.ID != id) || c.PID == skip {
			continue
		}
		if stopProcess(c.PID) == nil {
			n++
		}
		_ = os.Remove(drawEntryPath(c.PID))
	}
	return n
}

func coverUpdatePath(pid int) string {
	return filepath.Join(drawRegistryDir(), strconv.Itoa(pid)+".update")
}

// SendCoverUpdate asks the live covers with this id to merge patch into
// their style, and waits until each has taken it. A cover swaps its
// picture in one frame, so the area under it never shows.
func SendCoverUpdate(id string, patch CoverStyle) (int, error) {
	if _, err := patch.look(); err != nil {
		return 0, err
	}
	var pids []int
	for _, c := range LiveCovers() {
		if c.Cover.ID == id {
			pids = append(pids, c.PID)
		}
	}
	if len(pids) == 0 {
		return 0, fmt.Errorf("cover: no live cover with --id=%s (see `aos draw cover list`): %w", id, ErrNoMatch)
	}
	b, _ := json.Marshal(patch)
	for _, pid := range pids {
		tmp := coverUpdatePath(pid) + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err != nil {
			return 0, err
		}
		if err := os.Rename(tmp, coverUpdatePath(pid)); err != nil {
			return 0, err
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range pids {
		for {
			if _, err := os.Stat(coverUpdatePath(pid)); os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				_ = os.Remove(coverUpdatePath(pid))
				return 0, fmt.Errorf("cover %s (pid %d) did not take the update within 3s", id, pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return len(pids), nil
}

// TakeCoverUpdate returns (and consumes) an update sent to this process
// by SendCoverUpdate, or nil.
func TakeCoverUpdate() *CoverStyle {
	p := coverUpdatePath(os.Getpid())
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var s CoverStyle
	if json.Unmarshal(b, &s) != nil {
		_ = os.Remove(p)
		return nil
	}
	// Removed after it is shown: SendCoverUpdate returns once it is gone.
	return &s
}

// AckCoverUpdate tells SendCoverUpdate the update is on screen.
func AckCoverUpdate() { _ = os.Remove(coverUpdatePath(os.Getpid())) }
