package groups

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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

// `aos capture video` records the screen with ffmpeg and, when it stops,
// renders a zoomed "final" cut from what happened during the recording.
//
// Recording is run by a detached supervisor (`capture video supervise`, a
// hidden command) that owns ffmpeg's stdin — so `stop` can send the `q` that
// makes ffmpeg finalize the mp4 on every OS — and samples the cursor / active
// window for the follow and smart cameras. The supervisor and the CLI talk
// through a state file and a stop file under the aos config dir.

// videoEvent is one line of the events log. Rects and points are global screen
// coordinates; `t` is seconds since the recording started.
type videoEvent struct {
	T     float64       `json:"t"`
	Kind  string        `json:"kind"` // action, click, mark, focus, unfocus, follow, cursor, window
	Cmd   string        `json:"cmd,omitempty"`
	Rect  *engine.Rect  `json:"rect,omitempty"`
	At    *engine.Point `json:"at,omitempty"`
	Label string        `json:"label,omitempty"`
	Hold  float64       `json:"hold,omitempty"`
	Zoom  float64       `json:"zoom,omitempty"`
}

// videoState is the live recording, as the supervisor and CLI share it.
type videoState struct {
	PID       int           `json:"PID"`
	FFmpegPID int           `json:"FFmpegPID"`
	Out       string        `json:"Out"`
	Events    string        `json:"Events"` // the append-only .events.jsonl
	Started   time.Time     `json:"Started"`
	Ended     time.Time     `json:"Ended,omitempty"`
	Mode      string        `json:"Mode"`
	Follow    string        `json:"Follow,omitempty"`
	Match     *engine.Match `json:"Match,omitempty"`
	Monitor   engine.Rect   `json:"Monitor"`
	Capture   engine.Rect   `json:"Capture"`
	Zoom      float64       `json:"Zoom,omitempty"`
	Max       float64       `json:"Max"`
	FFArgs    []string      `json:"FFArgs"`
	Log       string        `json:"Log"`
	Finished  bool          `json:"Finished,omitempty"`
	Error     string        `json:"Error,omitempty"`
}

// videoDoc is the finished <out>.events.json: an open format, documented in
// the skill reference, that other tools can read.
type videoDoc struct {
	Version  int          `json:"version"`
	Out      string       `json:"out"`
	Started  time.Time    `json:"started"`
	Mode     string       `json:"mode"`
	Follow   string       `json:"follow,omitempty"`
	Monitor  engine.Rect  `json:"monitor"`
	Capture  engine.Rect  `json:"capture"`
	Zoom     float64      `json:"zoom,omitempty"`
	Offset   float64      `json:"offset"` // seconds the video started after `started`
	Duration float64      `json:"duration"`
	Events   []videoEvent `json:"events"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "capture", Name: "video start",
				Summary: "Start recording the screen with ffmpeg (detached; ask the person first)",
				Args: "[smart|monitor|window|region|app|mouse] [--monitor=<n>] [--app=<name>|--title=<s>] [--region=<x,y,w,h>] " +
					"[--fps=30] [--audio=none|mic|system|both] [--mic=<name|index>] [--zoom=2] [--out=<path.mp4>] [--max=10m] [--indicator=false] [--json]",
				Examples: []string{
					"aos capture video start smart --audio=mic --out=~/Movies/demo.mp4",
					"aos capture video start monitor --monitor=2",
					"aos capture video start mouse --zoom=2",
					"aos capture video start app --app=Chrome",
					"aos capture video start window --app=Terminal",
					"aos capture video start region --region=0,0,1280,720 --max=2m",
					"# exit 2 when ffmpeg (or a loopback device for --audio=system) is missing",
				},
				Run: runVideoStart,
			},
			&cli.Command{
				Group: "capture", Name: "video stop",
				Summary:  "Stop recording, finalize the mp4 and render <out>.final.mp4 with the auto camera",
				Args:     "[--raw] [--json]",
				Examples: []string{"aos capture video stop", "aos capture video stop --raw --json"},
				Blocking: true,
				Run:      runVideoStop,
			},
			&cli.Command{
				Group: "capture", Name: "video status",
				Summary:  "Is a recording running, and where is it going",
				Args:     "[--json]",
				Examples: []string{"aos capture video status"},
				Run:      runVideoStatus,
			},
			&cli.Command{
				Group: "capture", Name: "video mark",
				Summary:  "Add a labelled event to the recording (a camera target with --rect)",
				Args:     "<label...> [--rect=<x,y,w,h>]",
				Examples: []string{`aos capture video mark "login works"`, `aos capture video mark "the chart" --rect=200,150,800,450`},
				Run:      runVideoMark,
			},
			&cli.Command{
				Group: "capture", Name: "video focus",
				Summary: "Point the camera at a place until unfocus (or --hold)",
				Args: "(--rect=<x,y,w,h> | --at=<x,y> [--size=640x360] | --app=<name>|--title=<s> [--find=<text> [--role=<r>]]) " +
					"[--hold=<duration>] [--zoom=<factor>]",
				Examples: []string{
					"aos capture video focus --rect=100,100,800,450",
					"aos capture video focus --at=640,400 --size=800x450 --hold=4s",
					`aos capture video focus --app=Chrome --find="Sign in" --zoom=2`,
				},
				Run: runVideoFocus,
			},
			&cli.Command{
				Group: "capture", Name: "video unfocus",
				Summary:  "Send the camera back to its automatic mode",
				Examples: []string{"aos capture video unfocus"},
				Run:      runVideoUnfocus,
			},
			&cli.Command{
				Group: "capture", Name: "video follow",
				Summary:  "Switch the live camera: follow the mouse, an app, smart, or off",
				Args:     "mouse|app|smart|off [--app=<name>|--title=<s>]",
				Examples: []string{"aos capture video follow mouse", "aos capture video follow app --app=Chrome", "aos capture video follow off"},
				Run:      runVideoFollow,
			},
			&cli.Command{
				Group: "capture", Name: "video devices",
				Summary:  "List the screens, microphones and system-audio sources ffmpeg can record",
				Args:     "[--json]",
				Examples: []string{"aos capture video devices"},
				Run:      runVideoDevices,
			},
			&cli.Command{
				Group: "capture", Name: "video zoom",
				Summary: "Re-render an aos recording with the auto camera, from its events file",
				Args: "<in.mp4> [--events=<file>] [--out=<file>] [--pad=0.15] [--max-zoom=2.5] [--ease=0.6] [--hold-wide=1.0] " +
					"[--zoom=2] [--follow=mouse|app|smart|off] [--size=1920x1080] [--json]",
				Examples: []string{
					"aos capture video zoom ~/Movies/demo.mp4",
					"aos capture video zoom demo.mp4 --max-zoom=2 --ease=0.8 --out=demo.cut.mp4",
					"aos capture video zoom demo.mp4 --follow=off",
				},
				Blocking: true,
				Run:      runVideoZoom,
			},
			&cli.Command{
				Group: "capture", Name: "video supervise",
				Summary: "Internal: the detached process that runs ffmpeg for a recording",
				Hidden:  true,
				Run:     runVideoSupervise,
			},
		)
	})
}

// ---- options ---------------------------------------------------------------

// videoStartOptions is `capture video start` after parsing.
type videoStartOptions struct {
	Mode      string // monitor, window, region, app, mouse, smart
	Follow    string // "", mouse, window, smart
	Monitor   *int
	Match     engine.Match
	Region    *engine.Rect
	FPS       int
	Audio     audioMode
	Mic       string
	Zoom      float64
	Out       string
	Max       time.Duration
	Indicator bool
	JSON      bool
}

var videoStartFlags = []string{"monitor", "app", "title", "region", "fps", "audio", "mic", "zoom", "out", "max"}

func parseVideoStart(args []string) (videoStartOptions, error) {
	o := videoStartOptions{Mode: "monitor", FPS: 30, Max: 10 * time.Minute, Indicator: true, Zoom: 2}
	set, err := parseArgs(args, videoStartFlags...)
	if err != nil {
		return o, err
	}
	if err := set.Reject(append([]string{"indicator", "json"}, videoStartFlags...)...); err != nil {
		return o, err
	}
	if len(set.Rest) > 1 {
		return o, fmt.Errorf("`capture video start` takes one mode, got %q", strings.Join(set.Rest, " "))
	}
	if len(set.Rest) == 1 {
		switch m := strings.ToLower(set.Rest[0]); m {
		case "monitor", "fullscreen", "full", "screen":
			o.Mode = "monitor"
		case "window", "win":
			o.Mode = "window"
		case "region", "area":
			o.Mode = "region"
		case "app", "mouse", "smart":
			o.Mode = m
		default:
			return o, fmt.Errorf("unknown mode %q: use smart, monitor, window, region, app or mouse", set.Rest[0])
		}
	}
	o.Match = engine.Match{App: set.String("app", ""), Title: set.String("title", "")}
	hasMatch := o.Match.App != "" || o.Match.Title != ""
	switch o.Mode {
	case "window", "app":
		if !hasMatch {
			return o, fmt.Errorf("`%s` mode needs --app=<name> or --title=<s>", o.Mode)
		}
	case "region":
		raw := set.String("region", "")
		if raw == "" {
			return o, fmt.Errorf("`region` mode needs --region=x,y,w,h")
		}
		r, err := parseRect(raw)
		if err != nil {
			return o, err
		}
		o.Region = &r
	default:
		if hasMatch {
			return o, fmt.Errorf("--app/--title go with the window or app mode")
		}
	}
	if set.Has("region") && o.Mode != "region" {
		return o, fmt.Errorf("--region goes with the region mode")
	}
	switch o.Mode {
	case "mouse":
		o.Follow = "mouse"
	case "app":
		o.Follow = "window"
	case "smart":
		o.Follow = "smart"
	}
	if o.Monitor, err = set.IntPtr("monitor"); err != nil {
		return o, err
	}
	if o.FPS, err = set.Int("fps", 30); err != nil {
		return o, err
	}
	if o.FPS < 1 || o.FPS > 120 {
		return o, fmt.Errorf("--fps must be 1-120, got %d", o.FPS)
	}
	if o.Audio, err = parseAudioMode(set.String("audio", "none")); err != nil {
		return o, err
	}
	o.Mic = set.String("mic", "")
	if o.Mic != "" && o.Audio != audioMic && o.Audio != audioBoth {
		return o, fmt.Errorf("--mic goes with --audio=mic or --audio=both")
	}
	if raw := set.String("zoom", ""); raw != "" {
		if o.Zoom, err = strconv.ParseFloat(raw, 64); err != nil || o.Zoom < 1 || o.Zoom > 6 {
			return o, fmt.Errorf("--zoom must be a factor from 1 to 6, got %q", raw)
		}
	}
	if raw := set.String("max", ""); raw != "" {
		if o.Max, err = parseVideoDuration(raw); err != nil || o.Max <= 0 {
			return o, fmt.Errorf("--max must be a duration such as 90s or 10m, got %q", raw)
		}
	}
	o.Out = set.String("out", "")
	switch strings.ToLower(set.String("indicator", "true")) {
	case "false", "0", "off", "no":
		o.Indicator = false
	}
	o.JSON = set.Has("json")
	return o, nil
}

// parseVideoDuration takes a Go duration (90s, 10m, 1m30s) or plain seconds.
func parseVideoDuration(raw string) (time.Duration, error) {
	if secs, err := strconv.ParseFloat(raw, 64); err == nil {
		return time.Duration(secs * float64(time.Second)), nil
	}
	return time.ParseDuration(raw)
}

// ---- state and events -------------------------------------------------------

func videoDir(c *cli.Ctx) string { return filepath.Join(cli.ConfigDir(c.Env), "video") }

func videoStatePath(dir string) string { return filepath.Join(dir, "state.json") }
func videoStopPath(dir string) string  { return filepath.Join(dir, "stop") }

// readVideoState returns nil, nil when nothing is recording.
func readVideoState(dir string) (*videoState, error) {
	data, err := os.ReadFile(videoStatePath(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st videoState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("corrupt recording state %s: %w", videoStatePath(dir), err)
	}
	return &st, nil
}

// writeVideoState replaces the state atomically, so a reader never sees half.
func writeVideoState(dir string, st *videoState) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := videoStatePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, videoStatePath(dir))
}

// appendVideoEvent adds one JSON line. O_APPEND makes each small write land
// whole, so the supervisor and any command can log at the same time.
func appendVideoEvent(path string, ev videoEvent) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

func readVideoEvents(path string) ([]videoEvent, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []videoEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev videoEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out, sc.Err()
}

// activeRecording is the running recording, or nil.
func activeRecording(c *cli.Ctx) *videoState {
	st, err := readVideoState(videoDir(c))
	if err != nil || st == nil || st.Finished || st.Events == "" {
		return nil
	}
	return st
}

// noteVideo is the one hook other commands call: it appends an event to the
// active recording and is a silent no-op when nothing is recording.
func noteVideo(c *cli.Ctx, kind, cmd string, rect *engine.Rect, label string) {
	st := activeRecording(c)
	if st == nil {
		return
	}
	_ = appendVideoEvent(st.Events, videoEvent{T: since(st.Started), Kind: kind, Cmd: cmd, Rect: rect, Label: label})
}

// noteVideoWindow logs the bounds of a window a command just touched.
func noteVideoWindow(c *cli.Ctx, cmd string, match engine.Match) {
	if activeRecording(c) == nil {
		return
	}
	if w, ok := findWindow(match); ok {
		r := w.Bounds
		noteVideo(c, "action", cmd, &r, describeMatch(match))
	}
}

// noteVideoDraw logs a drawing as the union of its sign and what it points at,
// so the camera keeps both the arrow and the target in frame.
func noteVideoDraw(c *cli.Ctx, res engine.DrawResult, text string) {
	target := engine.Rect{X: res.Tip.X - 20, Y: res.Tip.Y - 20, W: 40, H: 40}
	if res.Found != nil {
		target = res.Found.Bounds
	}
	u := unionRect(res.Sign, target)
	noteVideo(c, "action", "draw", &u, text)
}

// noteVideoClick logs a small rect around where a click landed.
func noteVideoClick(c *cli.Ctx) {
	if activeRecording(c) == nil {
		return
	}
	if x, y, err := engine.CursorPosition(); err == nil {
		r := engine.Rect{X: x - 20, Y: y - 20, W: 40, H: 40}
		noteVideo(c, "click", "mouse click", &r, "")
	}
}

func unionRect(a, b engine.Rect) engine.Rect {
	if a.W <= 0 || a.H <= 0 {
		return b
	}
	if b.W <= 0 || b.H <= 0 {
		return a
	}
	x0, y0 := min(a.X, b.X), min(a.Y, b.Y)
	x1, y1 := max(a.X+a.W, b.X+b.W), max(a.Y+a.H, b.Y+b.H)
	return engine.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func since(t time.Time) float64 { return math.Round(time.Since(t).Seconds()*1000) / 1000 }

// ---- start ------------------------------------------------------------------

func ffmpegPath(goos string) (string, error) {
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", &cli.ExitError{Code: 2, Message: "ffmpeg is not on PATH; install it: " + ffmpegInstallHint(goos)}
	}
	return path, nil
}

func setupExit(err error) error {
	var missing *missingToolError
	if errors.As(err, &missing) {
		return &cli.ExitError{Code: 2, Message: missing.Error()}
	}
	return err
}

func runVideoStart(c *cli.Ctx, args []string) error {
	o, err := parseVideoStart(args)
	if err != nil {
		return err
	}
	ffmpeg, err := ffmpegPath(c.GOOS)
	if err != nil {
		return err
	}
	dir := videoDir(c)
	if st, _ := readVideoState(dir); st != nil {
		if !st.Finished && videoProcAlive(st.PID) {
			return &cli.ExitError{Code: 1, Message: fmt.Sprintf("already recording to %s; stop it with `aos capture video stop`", st.Out)}
		}
		// A recording whose supervisor is gone: keep what it captured.
		_, _ = finishVideo(st, dir)
	}

	monitor, capture, match, err := videoGeometry(o)
	if err != nil {
		return err
	}
	out, err := videoOutPath(o.Out, c.GOOS)
	if err != nil {
		return err
	}
	plan := ffPlan{GOOS: c.GOOS, FPS: o.FPS, MaxSec: o.Max.Seconds(), Out: out, Monitor: monitor.rect, Capture: capture, Display: c.Env("DISPLAY")}
	if err := resolvePlatformInputs(ffmpeg, &plan, o, monitor.id, match); err != nil {
		return setupExit(err)
	}
	ffargs, err := ffmpegRecordArgs(plan)
	if err != nil {
		return err
	}

	_ = os.Remove(videoStopPath(dir))
	st := &videoState{
		Out: out, Events: strings.TrimSuffix(out, filepath.Ext(out)) + ".events.jsonl",
		Mode: o.Mode, Follow: o.Follow, Monitor: monitor.rect, Capture: capture, Zoom: o.Zoom,
		Max: o.Max.Seconds(), FFArgs: append([]string{ffmpeg}, ffargs...), Log: filepath.Join(dir, "ffmpeg.log"),
		Started: time.Now(),
	}
	if match != nil {
		st.Match = match
	}
	_ = os.Remove(st.Events)
	if err := writeVideoState(dir, st); err != nil {
		return err
	}
	if err := spawnSupervisor(st.Log); err != nil {
		_ = os.Remove(videoStatePath(dir))
		return err
	}
	st, err = waitForRecording(dir, st.Log)
	if err != nil {
		return err
	}
	if o.Indicator {
		showRecordingIndicator()
	}

	res := map[string]any{"Out": st.Out, "PID": st.PID, "Started": st.Started, "Mode": st.Mode,
		"Origin": engine.Point{X: st.Capture.X, Y: st.Capture.Y}, "Capture": st.Capture, "Events": st.Events}
	if o.JSON {
		return json.NewEncoder(c.Stdout).Encode(res)
	}
	c.Printf("recording %s (%s mode) to %s — stop with `aos capture video stop`\n", describeRect(st.Capture), st.Mode, st.Out)
	return nil
}

type monitorPick struct {
	id   int
	rect engine.Rect
}

// videoGeometry picks the monitor to record and the rect the video shows.
func videoGeometry(o videoStartOptions) (monitorPick, engine.Rect, *engine.Match, error) {
	monitors, err := engine.ListMonitors()
	if err != nil || len(monitors) == 0 {
		return monitorPick{}, engine.Rect{}, nil, fmt.Errorf("cannot list monitors: %v", err)
	}
	rectOf := func(m engine.Monitor) engine.Rect {
		return engine.Rect{X: m.X, Y: m.Y, W: m.Width, H: m.Height}
	}
	byID := func(id int) (monitorPick, bool) {
		for _, m := range monitors {
			if m.ID == id {
				return monitorPick{m.ID, rectOf(m)}, true
			}
		}
		return monitorPick{}, false
	}
	var pick monitorPick
	var match *engine.Match
	var win *engine.Window
	if o.Mode == "window" || o.Mode == "app" {
		resolved, err := resolveMatch(o.Match)
		if err != nil {
			return pick, engine.Rect{}, nil, err
		}
		w, ok := findWindow(resolved)
		if !ok {
			return pick, engine.Rect{}, nil, &cli.ExitError{Code: exitNotFound, Message: "no window matches " + describeMatch(resolved)}
		}
		match, win = &resolved, &w
	}
	switch {
	case o.Monitor != nil:
		p, ok := byID(*o.Monitor)
		if !ok {
			return pick, engine.Rect{}, nil, fmt.Errorf("no monitor %d", *o.Monitor)
		}
		pick = p
	case win != nil && win.Monitor > 0:
		pick, _ = byID(win.Monitor)
	case o.Region != nil:
		cx, cy := o.Region.X+o.Region.W/2, o.Region.Y+o.Region.H/2
		for _, m := range monitors {
			if cx >= m.X && cx < m.X+m.Width && cy >= m.Y && cy < m.Y+m.Height {
				pick = monitorPick{m.ID, rectOf(m)}
			}
		}
	}
	if pick.id == 0 {
		for _, m := range monitors {
			if m.Focused {
				pick = monitorPick{m.ID, rectOf(m)}
			}
		}
	}
	if pick.id == 0 {
		pick = monitorPick{monitors[0].ID, rectOf(monitors[0])}
		for _, m := range monitors {
			if m.Primary {
				pick = monitorPick{m.ID, rectOf(m)}
			}
		}
	}
	capture := pick.rect
	switch {
	case o.Mode == "window":
		capture = intersectRect(win.Bounds, pick.rect)
	case o.Region != nil:
		capture = intersectRect(*o.Region, pick.rect)
	}
	if capture.W < 16 || capture.H < 16 {
		return pick, engine.Rect{}, nil, fmt.Errorf("the area to record (%s) is off monitor %d", describeRect(capture), pick.id)
	}
	return pick, capture, match, nil
}

func intersectRect(a, b engine.Rect) engine.Rect {
	x0, y0 := max(a.X, b.X), max(a.Y, b.Y)
	x1, y1 := min(a.X+a.W, b.X+b.W), min(a.Y+a.H, b.Y+b.H)
	if x1 <= x0 || y1 <= y0 {
		return engine.Rect{}
	}
	return engine.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

func describeRect(r engine.Rect) string {
	return fmt.Sprintf("%dx%d%s%s", r.W, r.H, signed(r.X), signed(r.Y))
}

// resolvePlatformInputs fills in what only this machine knows: the macOS screen
// device index, the window title for gdigrab, and the audio sources.
func resolvePlatformInputs(ffmpeg string, p *ffPlan, o videoStartOptions, monitor int, match *engine.Match) error {
	switch p.GOOS {
	case "darwin":
		video, audio := parseAVFDevices(listAVFDevices(ffmpeg))
		monitors, _ := engine.ListMonitors()
		guess := avfScreenNumber(monitors, monitor)
		idx, err := avfScreenIndex(video, guess)
		if err != nil {
			return err
		}
		// Verify the guess by the shape of a real frame; try the other screens
		// when it does not match the monitor.
		if w, h, ok := avfFrameSize(ffmpeg, idx); ok && !aspectMatches(w, h, p.Monitor) {
			for n := 0; n < len(monitors)+1; n++ {
				other, err := avfScreenIndex(video, n)
				if n == guess || err != nil {
					continue
				}
				if w, h, ok := avfFrameSize(ffmpeg, other); ok && aspectMatches(w, h, p.Monitor) {
					idx = other
					break
				}
			}
		}
		p.DarwinScreen = idx
		if o.Audio != audioNone {
			if p.Audio, err = darwinAudio(o.Audio, o.Mic, audio); err != nil {
				return err
			}
		}
	case "windows":
		if o.Mode == "window" && match != nil {
			if w, ok := findWindow(*match); ok {
				p.WinTitle = w.Title
			}
		}
		if o.Audio != audioNone {
			_, audio := parseDshowDevices(listDshowDevices(ffmpeg))
			a, err := windowsAudio(o.Audio, o.Mic, audio, ffmpegHasDevice(ffmpeg, "wasapi"))
			if err != nil {
				return err
			}
			p.Audio = a
		}
	case "linux":
		if o.Audio != audioNone {
			sink, _ := exec.Command("pactl", "get-default-sink").Output()
			p.Audio = linuxAudio(o.Audio, o.Mic, strings.TrimSpace(string(sink)))
		}
	}
	return nil
}

func listAVFDevices(ffmpeg string) string {
	out, _ := exec.Command(ffmpeg, "-hide_banner", "-f", "avfoundation", "-list_devices", "true", "-i", "").CombinedOutput()
	return string(out)
}

var frameSizeRe = regexp.MustCompile(`Video: .*?, (\d{2,5})x(\d{2,5})`)

// avfFrameSize grabs one frame from a screen device and reports its size.
func avfFrameSize(ffmpeg, idx string) (int, int, bool) {
	out, _ := exec.Command(ffmpeg, "-hide_banner", "-f", "avfoundation", "-framerate", "30",
		"-i", idx+":none", "-frames:v", "1", "-f", "null", "-").CombinedOutput()
	m := frameSizeRe.FindStringSubmatch(string(out))
	if m == nil {
		return 0, 0, false
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	return w, h, true
}

func listDshowDevices(ffmpeg string) string {
	cmd := exec.Command(ffmpeg, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	hideChild(cmd)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

func ffmpegHasDevice(ffmpeg, name string) bool {
	cmd := exec.Command(ffmpeg, "-hide_banner", "-devices")
	hideChild(cmd)
	out, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == name {
			return true
		}
	}
	return false
}

func videoOutPath(raw, goos string) (string, error) {
	if raw == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		folder := "Videos"
		if goos == "darwin" {
			folder = "Movies"
		}
		raw = filepath.Join(home, folder, "aos", "recording-"+time.Now().Format("2006-01-02-150405")+".mp4")
	}
	out, err := filepath.Abs(expandHome(raw))
	if err != nil {
		return "", err
	}
	if filepath.Ext(out) == "" {
		out += ".mp4"
	}
	return out, os.MkdirAll(filepath.Dir(out), 0o755)
}

func spawnSupervisor(logPath string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(self, "capture", "video", "supervise")
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// waitForRecording waits until ffmpeg has been running for a moment, so a bad
// device or a missing permission fails `start` instead of failing silently.
func waitForRecording(dir, logPath string) (*videoState, error) {
	deadline := time.Now().Add(5 * time.Second)
	var seen time.Time
	for time.Now().Before(deadline) {
		time.Sleep(150 * time.Millisecond)
		st, err := readVideoState(dir)
		if err != nil || st == nil {
			continue
		}
		if st.Finished {
			msg := st.Error
			if msg == "" {
				msg = logTail(logPath)
			}
			_, _ = finishVideo(st, dir)
			return nil, fmt.Errorf("ffmpeg stopped right away: %s", msg)
		}
		if st.FFmpegPID > 0 {
			if seen.IsZero() {
				seen = time.Now()
			} else if time.Since(seen) > 900*time.Millisecond {
				return st, nil
			}
		}
	}
	return nil, fmt.Errorf("the recorder did not start; see %s", logPath)
}

func logTail(path string) string {
	data, _ := os.ReadFile(path)
	s := strings.TrimSpace(string(data))
	if len(s) > 600 {
		s = s[len(s)-600:]
	}
	if s == "" {
		return "no output (check Screen Recording permission: `aos permission request screen`)"
	}
	return s
}

// showRecordingIndicator flashes a caption so the person knows the screen is
// being recorded. Best effort: no caption backend is not a failure.
func showRecordingIndicator() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "subtitle", "show", "● REC — aos is recording the screen", "--seconds=3", "--position=top")
	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
		time.Sleep(200 * time.Millisecond)
	}
}

// ---- supervise --------------------------------------------------------------

// runVideoSupervise is the detached recorder: it runs ffmpeg, samples the
// cursor and active window for follow modes, and on the stop file sends ffmpeg
// `q` so the mp4 is finalized.
func runVideoSupervise(c *cli.Ctx, args []string) error {
	dir := videoDir(c)
	st, err := readVideoState(dir)
	if err != nil || st == nil {
		return fmt.Errorf("no recording to supervise")
	}
	cmd := exec.Command(st.FFArgs[0], st.FFArgs[1:]...)
	hideChild(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Stdout, cmd.Stderr = c.Stdout, c.Stderr
	if err := cmd.Start(); err != nil {
		st.Finished, st.Error = true, err.Error()
		return writeVideoState(dir, st)
	}
	st.PID, st.FFmpegPID, st.Started = os.Getpid(), cmd.Process.Pid, time.Now()
	if err := writeVideoState(dir, st); err != nil {
		_ = cmd.Process.Kill()
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	sampler := newVideoSampler(st)
	tick := time.NewTicker(80 * time.Millisecond)
	defer tick.Stop()
	var exitErr error
	stopped := false
	for n := 0; ; n++ {
		select {
		case exitErr = <-done:
			goto finished
		case <-tick.C:
		}
		if fileExists(videoStopPath(dir)) {
			stopped = true
			break
		}
		if n%12 == 0 {
			if fresh, err := readVideoState(dir); err == nil && fresh != nil {
				sampler.follow, sampler.match = fresh.Follow, fresh.Match
			}
		}
		sampler.sample(n)
	}
	_, _ = io.WriteString(stdin, "q")
	select {
	case exitErr = <-done:
	case <-time.After(10 * time.Second):
		_ = interrupt(cmd.Process)
		select {
		case exitErr = <-done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			exitErr = <-done
		}
	}
finished:
	_ = stdin.Close()
	if fresh, err := readVideoState(dir); err == nil && fresh != nil {
		st = fresh
	}
	st.Finished, st.Ended = true, time.Now()
	if exitErr != nil && !stopped {
		st.Error = strings.TrimSpace(exitErr.Error() + ": " + logTail(st.Log))
	}
	_ = os.Remove(videoStopPath(dir))
	return writeVideoState(dir, st)
}

// videoSampler logs the cursor (~12 Hz) and the followed window (~3 Hz) while
// a follow mode is on. It only writes when something changed.
type videoSampler struct {
	st       *videoState
	follow   string
	match    *engine.Match
	lastX    int
	lastY    int
	lastWin  engine.Rect
	hasPoint bool
}

func newVideoSampler(st *videoState) *videoSampler {
	return &videoSampler{st: st, follow: st.Follow, match: st.Match}
}

func (s *videoSampler) sample(n int) {
	if s.follow == "mouse" || s.follow == "smart" {
		if x, y, err := engine.CursorPosition(); err == nil && (!s.hasPoint || x != s.lastX || y != s.lastY) {
			s.lastX, s.lastY, s.hasPoint = x, y, true
			_ = appendVideoEvent(s.st.Events, videoEvent{T: since(s.st.Started), Kind: "cursor", At: &engine.Point{X: x, Y: y}})
		}
	}
	if n%4 != 0 {
		return
	}
	var win engine.Window
	var ok bool
	switch {
	case s.follow == "window" && s.match != nil:
		win, ok = findWindow(*s.match)
	case s.follow == "smart":
		if windows, err := engine.ListWindows(engine.Filter{}); err == nil {
			for _, w := range windows {
				if w.Focused {
					win, ok = w, true
					break
				}
			}
		}
	}
	if ok && win.Bounds != s.lastWin {
		s.lastWin = win.Bounds
		r := win.Bounds
		_ = appendVideoEvent(s.st.Events, videoEvent{T: since(s.st.Started), Kind: "window", Rect: &r, Label: win.App})
	}
}

// ---- stop / status ----------------------------------------------------------

func runVideoStop(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("raw", "json"); err != nil {
		return err
	}
	dir := videoDir(c)
	st, err := readVideoState(dir)
	if err != nil {
		return err
	}
	if st == nil {
		return &cli.ExitError{Code: 1, Message: "not recording; start with `aos capture video start`"}
	}
	if !st.Finished {
		if err := os.WriteFile(videoStopPath(dir), nil, 0o644); err != nil {
			return err
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			time.Sleep(150 * time.Millisecond)
			fresh, _ := readVideoState(dir)
			if fresh != nil && fresh.Finished {
				st = fresh
				break
			}
			if !videoProcAlive(st.PID) {
				break
			}
		}
	}
	doc, err := finishVideo(st, dir)
	if err != nil {
		return err
	}
	res := map[string]any{"Out": st.Out, "Events": eventsJSONPath(st.Out), "Duration": doc.Duration}
	var renderErr error
	if !set.Has("raw") {
		final := finalPath(st.Out)
		if renderErr = renderVideo(st.Out, doc, zoomOptions{}, final); renderErr == nil {
			res["Final"] = final
		} else {
			res["RenderError"] = renderErr.Error()
		}
	}
	if set.Has("json") {
		if err := json.NewEncoder(c.Stdout).Encode(res); err != nil {
			return err
		}
	} else {
		c.Printf("raw:    %s (%.1fs)\n", st.Out, doc.Duration)
		if f, ok := res["Final"]; ok {
			c.Printf("final:  %s\n", f)
		}
		c.Printf("events: %s\n", eventsJSONPath(st.Out))
	}
	if renderErr != nil {
		return &cli.ExitError{Code: 1, Message: "rendering the final cut failed (the raw recording is kept): " + renderErr.Error()}
	}
	return nil
}

func eventsJSONPath(out string) string {
	return strings.TrimSuffix(out, filepath.Ext(out)) + ".events.json"
}
func finalPath(out string) string { return strings.TrimSuffix(out, filepath.Ext(out)) + ".final.mp4" }

// finishVideo writes <out>.events.json from the live log and clears the
// recording state. The video's real duration fixes the start offset: ffmpeg
// begins producing frames a little after the supervisor marks the start.
func finishVideo(st *videoState, dir string) (videoDoc, error) {
	events, err := readVideoEvents(st.Events)
	if err != nil {
		return videoDoc{}, err
	}
	doc := videoDoc{Version: 1, Out: st.Out, Started: st.Started, Mode: st.Mode, Follow: st.Follow,
		Monitor: st.Monitor, Capture: st.Capture, Zoom: st.Zoom, Events: events}
	if doc.Events == nil {
		doc.Events = []videoEvent{}
	}
	if info, err := probeVideo(st.Out); err == nil {
		doc.Duration = info.Duration
		if !st.Ended.IsZero() {
			doc.Offset = videoOffset(st.Ended.Sub(st.Started).Seconds(), info.Duration)
		}
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return doc, err
	}
	if err := os.WriteFile(eventsJSONPath(st.Out), data, 0o644); err != nil {
		return doc, err
	}
	_ = os.Remove(st.Events)
	_ = os.Remove(videoStatePath(dir))
	if st.Error != "" && doc.Duration == 0 {
		return doc, fmt.Errorf("recording failed: %s", st.Error)
	}
	return doc, nil
}

// videoOffset is how long after the recorded start the first frame came:
// wall-clock length minus video length, kept within a sane startup window.
func videoOffset(wall, video float64) float64 {
	off := wall - video
	if off < 0 || off > 5 {
		return 0
	}
	return math.Round(off*1000) / 1000
}

func runVideoStatus(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	st, err := readVideoState(videoDir(c))
	if err != nil {
		return err
	}
	status := map[string]any{"Recording": false}
	if st != nil {
		alive := !st.Finished && videoProcAlive(st.PID)
		status = map[string]any{"Recording": alive, "Out": st.Out, "Mode": st.Mode, "Follow": st.Follow,
			"Started": st.Started, "Elapsed": since(st.Started), "Max": st.Max, "Capture": st.Capture}
		if !alive {
			status["Note"] = "the recorder has ended; run `aos capture video stop` to finish the file"
		}
	}
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(status)
	}
	if st == nil {
		c.Println("not recording")
		return nil
	}
	state := "recording"
	if !status["Recording"].(bool) {
		state = "ended (run `aos capture video stop`)"
	}
	c.Printf("%s %s for %.0fs (max %.0fs), %s mode -> %s\n", state, describeRect(st.Capture), since(st.Started), st.Max, st.Mode, st.Out)
	return nil
}

// ---- mark / focus / unfocus / follow ----------------------------------------

func requireRecording(c *cli.Ctx) (*videoState, error) {
	st := activeRecording(c)
	if st == nil {
		return nil, &cli.ExitError{Code: 1, Message: "not recording; start with `aos capture video start`"}
	}
	return st, nil
}

func runVideoMark(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "rect")
	if err != nil {
		return err
	}
	if err := set.Reject("rect"); err != nil {
		return err
	}
	label := strings.Join(set.Rest, " ")
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("`capture video mark` needs a label")
	}
	st, err := requireRecording(c)
	if err != nil {
		return err
	}
	ev := videoEvent{T: since(st.Started), Kind: "mark", Cmd: "capture video mark", Label: label}
	if raw := set.String("rect", ""); raw != "" {
		r, err := parseRect(raw)
		if err != nil {
			return err
		}
		ev.Rect = &r
	}
	if err := appendVideoEvent(st.Events, ev); err != nil {
		return err
	}
	c.Printf("marked %q at %.1fs\n", label, ev.T)
	return nil
}

// videoFocusSpec is `capture video focus` after parsing.
type videoFocusSpec struct {
	Rect  *engine.Rect
	Match engine.Match
	Find  engine.FindOptions
	Hold  float64
	Zoom  float64
}

var videoFocusFlags = []string{"rect", "at", "size", "app", "title", "find", "role", "source", "hold", "zoom"}

func parseVideoFocus(args []string) (videoFocusSpec, error) {
	var f videoFocusSpec
	set, err := parseArgs(args, videoFocusFlags...)
	if err != nil {
		return f, err
	}
	if err := set.Reject(append([]string{"exact"}, videoFocusFlags...)...); err != nil {
		return f, err
	}
	if len(set.Rest) > 0 {
		return f, fmt.Errorf("unexpected %q; say where with --rect, --at or --app", strings.Join(set.Rest, " "))
	}
	targets := 0
	if raw := set.String("rect", ""); raw != "" {
		r, err := parseRect(raw)
		if err != nil {
			return f, err
		}
		f.Rect, targets = &r, targets+1
	}
	if raw := set.String("at", ""); raw != "" {
		p, err := drawInts(raw, 2, "--at=x,y")
		if err != nil {
			return f, err
		}
		size, err := drawInts(strings.ReplaceAll(set.String("size", "640x360"), "x", ","), 2, "--size=WxH")
		if err != nil {
			return f, err
		}
		f.Rect = &engine.Rect{X: p[0] - size[0]/2, Y: p[1] - size[1]/2, W: size[0], H: size[1]}
		targets++
	} else if set.Has("size") {
		return f, fmt.Errorf("--size goes with --at")
	}
	f.Match = engine.Match{App: set.String("app", ""), Title: set.String("title", "")}
	if f.Match.App != "" || f.Match.Title != "" {
		targets++
	}
	if text := set.String("find", ""); text != "" {
		if f.Match.App == "" && f.Match.Title == "" {
			return f, fmt.Errorf("--find needs --app or --title: the window to search")
		}
		f.Find = engine.FindOptions{Text: text, Role: set.String("role", ""),
			Source: engine.FindSource(set.String("source", "")), Exact: set.Has("exact")}
	}
	if targets != 1 {
		return f, fmt.Errorf("give exactly one of --rect, --at, or --app/--title")
	}
	if raw := set.String("hold", ""); raw != "" {
		d, err := parseVideoDuration(raw)
		if err != nil || d <= 0 {
			return f, fmt.Errorf("--hold must be a duration such as 3s, got %q", raw)
		}
		f.Hold = d.Seconds()
	}
	if raw := set.String("zoom", ""); raw != "" {
		if f.Zoom, err = strconv.ParseFloat(raw, 64); err != nil || f.Zoom < 1 || f.Zoom > 6 {
			return f, fmt.Errorf("--zoom must be a factor from 1 to 6, got %q", raw)
		}
	}
	return f, nil
}

func runVideoFocus(c *cli.Ctx, args []string) error {
	f, err := parseVideoFocus(args)
	if err != nil {
		return err
	}
	st, err := requireRecording(c)
	if err != nil {
		return err
	}
	label := ""
	if f.Rect == nil {
		match, err := resolveMatch(f.Match)
		if err != nil {
			return err
		}
		if f.Find.Text != "" {
			// The same path as `aos find`.
			f.Find.Match = match
			hits, err := engine.FindText(f.Find)
			if err != nil {
				return notFoundExit(err)
			}
			r := hits[0].Bounds
			f.Rect, label = &r, hits[0].Label
		} else {
			w, ok := findWindow(match)
			if !ok {
				return &cli.ExitError{Code: exitNotFound, Message: "no window matches " + describeMatch(match)}
			}
			r := w.Bounds
			f.Rect, label = &r, w.App
		}
	}
	ev := videoEvent{T: since(st.Started), Kind: "focus", Cmd: "capture video focus", Rect: f.Rect, Hold: f.Hold, Zoom: f.Zoom, Label: label}
	if err := appendVideoEvent(st.Events, ev); err != nil {
		return err
	}
	c.Printf("camera on %s at %.1fs\n", describeRect(*f.Rect), ev.T)
	return nil
}

func runVideoUnfocus(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`capture video unfocus` takes no arguments")
	}
	st, err := requireRecording(c)
	if err != nil {
		return err
	}
	return appendVideoEvent(st.Events, videoEvent{T: since(st.Started), Kind: "unfocus", Cmd: "capture video unfocus"})
}

// parseFollowMode maps the user's word onto the stored follow mode.
func parseFollowMode(raw string) (string, error) {
	switch strings.ToLower(raw) {
	case "mouse", "cursor":
		return "mouse", nil
	case "app", "window":
		return "window", nil
	case "smart":
		return "smart", nil
	case "off", "none", "wide":
		return "off", nil
	}
	return "", fmt.Errorf("follow mode must be mouse, app, smart or off, got %q", raw)
}

func runVideoFollow(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "title")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "title"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`capture video follow` takes one of mouse, app, smart, off")
	}
	mode, err := parseFollowMode(set.Rest[0])
	if err != nil {
		return err
	}
	st, err := requireRecording(c)
	if err != nil {
		return err
	}
	if mode == "window" {
		match, ok := optionalMatch(set)
		if !ok {
			return fmt.Errorf("`follow app` needs --app=<name> or --title=<s>")
		}
		st.Match = &match
	}
	st.Follow = mode
	if err := writeVideoState(videoDir(c), st); err != nil {
		return err
	}
	return appendVideoEvent(st.Events, videoEvent{T: since(st.Started), Kind: "follow", Cmd: "capture video follow", Label: mode})
}

// ---- devices ----------------------------------------------------------------

func runVideoDevices(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	ffmpeg, err := ffmpegPath(c.GOOS)
	if err != nil {
		return err
	}
	devices := map[string][]string{"Screens": {}, "Mics": {}, "System": {}}
	switch c.GOOS {
	case "darwin":
		video, audio := parseAVFDevices(listAVFDevices(ffmpeg))
		for _, d := range video {
			if strings.HasPrefix(d.Name, "Capture screen") {
				devices["Screens"] = append(devices["Screens"], fmt.Sprintf("%d: %s (monitor %s)", d.Index, d.Name, monitorOfScreen(d.Name)))
			}
		}
		for _, d := range audio {
			key := "Mics"
			if isLoopback(d.Name) {
				key = "System"
			}
			devices[key] = append(devices[key], fmt.Sprintf("%d: %s", d.Index, d.Name))
		}
	case "windows":
		devices["Screens"] = []string{"desktop (gdigrab)"}
		_, audio := parseDshowDevices(listDshowDevices(ffmpeg))
		for _, d := range audio {
			key := "Mics"
			if isWindowsLoopback(d) {
				key = "System"
			}
			devices[key] = append(devices[key], d)
		}
		if ffmpegHasDevice(ffmpeg, "wasapi") {
			devices["System"] = append(devices["System"], "wasapi loopback")
		}
	case "linux":
		devices["Screens"] = []string{c.Env("DISPLAY") + " (x11grab)"}
		out, _ := exec.Command("pactl", "list", "short", "sources").Output()
		for _, line := range strings.Split(string(out), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			key := "Mics"
			if strings.HasSuffix(f[1], ".monitor") {
				key = "System"
			}
			devices[key] = append(devices[key], f[1])
		}
	}
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(devices)
	}
	for _, k := range []string{"Screens", "Mics", "System"} {
		c.Printf("%s:\n", k)
		if len(devices[k]) == 0 {
			c.Println("  (none)")
		}
		for _, d := range devices[k] {
			c.Printf("  %s\n", d)
		}
	}
	return nil
}

func monitorOfScreen(name string) string {
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(name, "Capture screen")))
	if err != nil {
		return "?"
	}
	return strconv.Itoa(n + 1)
}

// ---- zoom / render ----------------------------------------------------------

func parseZoomOptions(set *argSet) (zoomOptions, error) {
	var o zoomOptions
	floats := []struct {
		flag string
		dst  *float64
	}{{"pad", &o.Pad}, {"max-zoom", &o.MaxZoom}, {"ease", &o.Ease}, {"hold-wide", &o.HoldWide}, {"zoom", &o.FollowZoom}}
	for _, f := range floats {
		raw := set.String(f.flag, "")
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 {
			return o, fmt.Errorf("--%s must be a non-negative number, got %q", f.flag, raw)
		}
		*f.dst = v
	}
	if set.Has("hold-wide") && o.HoldWide == 0 {
		o.HoldWide = -1 // an explicit 0: no wide hold
	}
	if o.MaxZoom != 0 && o.MaxZoom < 1 {
		return o, fmt.Errorf("--max-zoom must be at least 1")
	}
	if raw := set.String("follow", ""); raw != "" {
		mode, err := parseFollowMode(raw)
		if err != nil {
			return o, err
		}
		o.Follow = mode
	}
	if raw := set.String("size", ""); raw != "" {
		n, err := drawInts(strings.ReplaceAll(raw, "x", ","), 2, "--size=WxH")
		if err != nil || n[0] < 64 || n[1] < 64 {
			return o, fmt.Errorf("--size must be WxH, such as 1920x1080")
		}
		o.OutW, o.OutH = even(n[0]), even(n[1])
	}
	return o, nil
}

func runVideoZoom(c *cli.Ctx, args []string) error {
	valueFlags := []string{"events", "out", "pad", "max-zoom", "ease", "hold-wide", "zoom", "follow", "size"}
	set, err := parseArgs(args, valueFlags...)
	if err != nil {
		return err
	}
	if err := set.Reject(append([]string{"json"}, valueFlags...)...); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`capture video zoom` takes one recording: <in.mp4>")
	}
	if _, err := ffmpegPath(c.GOOS); err != nil {
		return err
	}
	in := expandHome(set.Rest[0])
	o, err := parseZoomOptions(set)
	if err != nil {
		return err
	}
	eventsPath := expandHome(set.String("events", eventsJSONPath(in)))
	data, err := os.ReadFile(eventsPath)
	if err != nil {
		return fmt.Errorf("no events file for %s (%v); zoom re-renders aos recordings from their <name>.events.json", in, err)
	}
	var doc videoDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("bad events file %s: %w", eventsPath, err)
	}
	out := expandHome(set.String("out", finalPath(in)))
	if err := renderVideo(in, doc, o, out); err != nil {
		return err
	}
	if set.Has("json") {
		return json.NewEncoder(c.Stdout).Encode(map[string]string{"Out": out})
	}
	c.Println(out)
	return nil
}

// videoInfo is what ffprobe says about a file.
type videoInfo struct {
	W, H     int
	Duration float64
	Audio    bool
}

func probeVideo(path string) (videoInfo, error) {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,width,height:format=duration", "-of", "json", path)
	hideChild(cmd)
	out, err := cmd.Output()
	if err != nil {
		return videoInfo{}, fmt.Errorf("ffprobe %s: %w", path, err)
	}
	return parseProbe(out)
}

func parseProbe(out []byte) (videoInfo, error) {
	var p struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return videoInfo{}, err
	}
	var info videoInfo
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if info.W == 0 {
				info.W, info.H = s.Width, s.Height
			}
		case "audio":
			info.Audio = true
		}
	}
	info.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	if info.W == 0 || info.H == 0 {
		return info, fmt.Errorf("no video stream")
	}
	return info, nil
}

// renderVideo renders the auto-camera cut of an aos recording.
func renderVideo(in string, doc videoDoc, o zoomOptions, out string) error {
	info, err := probeVideo(in)
	if err != nil {
		return err
	}
	o = o.withDefaults()
	if doc.Zoom > 0 && o.FollowZoom == 2 {
		o.FollowZoom = doc.Zoom
	}
	g := newCamGeom(o.OutW, o.OutH, info.W, info.H, doc.Capture)
	keys := simplifyKeys(planCamera(doc, g, o, info.Duration), 0.75)
	rings, times := clickRings(doc, g)
	script, err := os.CreateTemp("", "aos-camera-*.txt")
	if err != nil {
		return err
	}
	defer os.Remove(script.Name())
	if _, err := script.WriteString(cameraFilter(keys, g, rings, times)); err != nil {
		return err
	}
	script.Close()
	cmd := exec.Command("ffmpeg", renderArgs(in, script.Name(), out, info.Audio)...)
	hideChild(cmd)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(msg)))
	}
	return nil
}
