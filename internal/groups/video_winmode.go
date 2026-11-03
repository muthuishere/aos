package groups

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// `capture video start windows` (macOS) records each named window on its own
// through ScreenCaptureKit, so only that window's pixels are captured: another
// window on top, a notification or the desktop never reaches the video. Each
// window gets its own mp4; at stop they are joined side by side into <out>
// (and <out>.final.mp4, with any `aos say` narration mixed in). The per-window
// files stay, for cutting between scenes.

// winTarget is one window to record.
type winTarget struct {
	ID    uint32
	App   string
	Title string
	Out   string
	W, H  float64 // points, from ScreenCaptureKit
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func winSlug(s string) string {
	s = strings.Trim(slugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if s == "" {
		return "window"
	}
	return s
}

// resolveWindowTargets finds one window per --app (or the --title window).
func resolveWindowTargets(o videoStartOptions, out string) ([]winTarget, error) {
	var matches []engine.Match
	if len(o.Apps) > 0 {
		for _, a := range o.Apps {
			matches = append(matches, engine.Match{App: a, Title: o.Match.Title})
		}
	} else {
		matches = []engine.Match{o.Match}
	}
	base := strings.TrimSuffix(out, filepath.Ext(out))
	var targets []winTarget
	seen := map[uint32]bool{}
	for i, m := range matches {
		resolved, err := resolveMatch(m)
		if err != nil {
			return nil, err
		}
		w, ok := findWindow(resolved)
		if !ok {
			return nil, &cli.ExitError{Code: exitNotFound, Message: "no window matches " + describeMatch(resolved)}
		}
		id, err := strconv.ParseUint(w.ID, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("window %q has no numeric id", w.ID)
		}
		if seen[uint32(id)] {
			return nil, fmt.Errorf("%s and an earlier --app are the same window", describeMatch(resolved))
		}
		seen[uint32(id)] = true
		targets = append(targets, winTarget{ID: uint32(id), App: w.App, Title: w.Title,
			Out: fmt.Sprintf("%s.%d-%s.mp4", base, i+1, winSlug(w.App))})
	}
	ids := make([]uint32, len(targets))
	for i, t := range targets {
		ids[i] = t.ID
	}
	sizes, err := sckWindowSizes(ids)
	if err != nil {
		return nil, err
	}
	for i := range targets {
		targets[i].W, targets[i].H = sizes[i][0], sizes[i][1]
		if sizes[i][0] == 0 {
			return nil, fmt.Errorf("%s (%s) is not a window ScreenCaptureKit can record", targets[i].App, targets[i].Title)
		}
	}
	return targets, nil
}

func runWindowsStart(c *cli.Ctx, o videoStartOptions) error {
	if !winRecSupported {
		return &cli.ExitError{Code: 2, Message: errWinRecMsg()}
	}
	if _, err := ffmpegPath(c.GOOS); err != nil { // joins the files at stop
		return err
	}
	out, err := videoOutPath(o.Out, c.GOOS)
	if err != nil {
		return err
	}
	targets, err := resolveWindowTargets(o, out)
	if err != nil {
		return err
	}
	if o.DryRun {
		if o.JSON {
			return json.NewEncoder(c.Stdout).Encode(map[string]any{"Out": out, "Windows": targets, "Recorded": false})
		}
		for _, t := range targets {
			c.Printf("would record window %d  %s  %q  %.0fx%.0f -> %s\n", t.ID, t.App, t.Title, t.W, t.H, t.Out)
		}
		c.Printf("joined -> %s (dry run: nothing recorded)\n", out)
		return nil
	}
	dir := videoDir(c)
	if st, _ := readVideoState(dir); st != nil {
		if !st.Finished && videoProcAlive(st.PID) {
			return &cli.ExitError{Code: 1, Message: fmt.Sprintf("already recording to %s; stop it with `aos capture video stop`", st.Out)}
		}
		_, _ = finishVideo(st, dir)
	}
	_ = os.Remove(videoStopPath(dir))
	st := &videoState{
		Out: out, Events: strings.TrimSuffix(out, filepath.Ext(out)) + ".events.jsonl",
		Mode: "windows", Max: o.Max.Seconds(), Log: filepath.Join(dir, "ffmpeg.log"), Started: time.Now(),
	}
	for _, t := range targets {
		st.WinIDs, st.WinOuts = append(st.WinIDs, t.ID), append(st.WinOuts, t.Out)
	}
	st.FFArgs = []string{strconv.Itoa(o.FPS)} // the frame rate, for the supervisor
	_ = os.Remove(st.Events)
	if err := writeVideoState(dir, st); err != nil {
		return err
	}
	if err := spawnSupervisor(st.Log); err != nil {
		_ = os.Remove(videoStatePath(dir))
		return err
	}
	if st, err = waitForRecording(dir, st.Log); err != nil {
		return err
	}
	if o.JSON {
		return json.NewEncoder(c.Stdout).Encode(map[string]any{"Out": st.Out, "PID": st.PID, "Mode": st.Mode, "Windows": st.WinOuts, "Events": st.Events})
	}
	for _, t := range targets {
		c.Printf("recording window %s (%s) -> %s\n", t.App, t.Title, t.Out)
	}
	c.Printf("stop with `aos capture video stop` (joined into %s)\n", st.Out)
	return nil
}

// superviseWindows runs in the detached supervisor: it owns the streams until
// the stop file appears or --max passes, then finishes the files and joins them.
func superviseWindows(dir string, st *videoState) error {
	fps := 30
	if len(st.FFArgs) == 1 {
		if n, err := strconv.Atoi(st.FFArgs[0]); err == nil && n > 0 {
			fps = n
		}
	}
	if err := sckStart(st.WinIDs, st.WinOuts, fps, 2); err != nil {
		st.Finished, st.Error = true, err.Error()
		_, _ = sckStop()
		return writeVideoState(dir, st)
	}
	// The streams live in this process, which stands in for ffmpeg here.
	st.PID, st.FFmpegPID, st.Started = os.Getpid(), os.Getpid(), time.Now()
	if err := writeVideoState(dir, st); err != nil {
		_, _ = sckStop()
		return err
	}
	deadline := time.Now().Add(time.Duration(st.Max * float64(time.Second)))
	var runErr error
	for !fileExists(videoStopPath(dir)) && time.Now().Before(deadline) {
		if runErr = sckFailure(); runErr != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	frames, stopErr := sckStop()
	if fresh, err := readVideoState(dir); err == nil && fresh != nil {
		st = fresh
	}
	st.Finished, st.Ended = true, time.Now()
	switch {
	case runErr != nil:
		st.Error = runErr.Error()
	case stopErr != nil:
		st.Error = stopErr.Error()
	case frames == 0:
		st.Error = "no frames were captured (is Screen Recording allowed for this terminal?)"
	}
	if st.Error == "" || frames > 0 {
		if err := joinWindowFiles(st.WinOuts, st.Out, fps); err != nil && st.Error == "" {
			st.Error = err.Error()
		}
	}
	_ = os.Remove(videoStopPath(dir))
	return writeVideoState(dir, st)
}

// joinArgs lays the window files side by side at one height (one file is
// re-encoded as is), at a constant frame rate.
func joinArgs(ins []string, out string, fps int, height int) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	for _, in := range ins {
		args = append(args, "-i", in)
	}
	var f strings.Builder
	for i := range ins {
		fmt.Fprintf(&f, "[%d:v]fps=%d,scale=-2:%d,setsar=1[v%d];", i, fps, height, i)
	}
	if len(ins) == 1 {
		f.WriteString("[v0]format=yuv420p[v]")
	} else {
		for i := range ins {
			fmt.Fprintf(&f, "[v%d]", i)
		}
		fmt.Fprintf(&f, "hstack=inputs=%d:shortest=0,format=yuv420p[v]", len(ins))
	}
	return append(args, "-filter_complex", f.String(), "-map", "[v]",
		"-c:v", "libx264", "-preset", "medium", "-crf", "20", "-movflags", "+faststart", out)
}

func joinWindowFiles(ins []string, out string, fps int) error {
	var have []string
	for _, in := range ins {
		if fileExists(in) {
			have = append(have, in)
		}
	}
	if len(have) == 0 {
		return fmt.Errorf("no window file was written")
	}
	cmd := exec.Command("ffmpeg", joinArgs(have, out, fps, 1080)...)
	hideChild(cmd)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("joining the window files: %v: %s", err, strings.TrimSpace(string(msg)))
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func errWinRecMsg() string {
	return "`windows` mode records with ScreenCaptureKit and is macOS only; use `window` mode here"
}
