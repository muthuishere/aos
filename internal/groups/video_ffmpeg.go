package groups

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/engine"
)

// This file holds the pure half of `capture video`: turning a recording plan
// into an ffmpeg command line for each OS, and reading ffmpeg's device lists.
// Nothing here touches the screen or runs a process, so every branch is tested
// on every OS.

// audioMode is what `--audio` asks to record next to the screen.
type audioMode string

const (
	audioNone   audioMode = "none"
	audioMic    audioMode = "mic"
	audioSystem audioMode = "system"
	audioBoth   audioMode = "both"
)

func parseAudioMode(raw string) (audioMode, error) {
	switch m := audioMode(strings.ToLower(strings.TrimSpace(raw))); m {
	case "", audioNone:
		return audioNone, nil
	case audioMic, audioSystem, audioBoth:
		return m, nil
	}
	return "", fmt.Errorf("--audio must be none, mic, system or both, got %q", raw)
}

// audioInput is one ffmpeg audio input: its demuxer and its -i value.
type audioInput struct {
	Format string `json:"Format"`
	Input  string `json:"Input"`
}

// ffPlan is everything ffmpeg needs to record. Rects are global screen
// coordinates in the OS's logical units.
type ffPlan struct {
	GOOS    string
	FPS     int
	MaxSec  float64
	Out     string
	Monitor engine.Rect // the monitor being recorded
	Capture engine.Rect // the part of it that ends up in the video
	// DarwinScreen is the avfoundation video device index of the monitor.
	DarwinScreen string
	// WinTitle records one window by title through gdigrab (Windows only).
	WinTitle string
	Display  string // X11 display for x11grab, e.g. ":0"
	Audio    []audioInput
}

// cropped reports whether the capture is a part of the monitor.
func (p ffPlan) cropped() bool { return p.Capture != p.Monitor && p.Capture.W > 0 }

// ffmpegRecordArgs builds the ffmpeg argv (without the binary) for a plan.
//
// macOS records the whole screen device and crops. avfoundation captures
// physical pixels, so the crop is written as fractions of the input size
// (iw*W/MonitorW) — that makes it correct at any backing scale without having
// to know the scale up front.
func ffmpegRecordArgs(p ffPlan) ([]string, error) {
	if p.FPS <= 0 {
		p.FPS = 30
	}
	fps := strconv.Itoa(p.FPS)
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}

	switch p.GOOS {
	case "darwin":
		if p.DarwinScreen == "" {
			return nil, fmt.Errorf("no avfoundation screen device for the monitor")
		}
		args = append(args, "-thread_queue_size", "1024", "-f", "avfoundation",
			"-capture_cursor", "1", "-capture_mouse_clicks", "1",
			"-framerate", fps, "-i", p.DarwinScreen+":none")
	case "windows":
		args = append(args, "-thread_queue_size", "1024", "-f", "gdigrab", "-framerate", fps, "-draw_mouse", "1")
		if p.WinTitle != "" {
			args = append(args, "-i", "title="+p.WinTitle)
		} else {
			r := p.Capture
			args = append(args, "-offset_x", strconv.Itoa(r.X), "-offset_y", strconv.Itoa(r.Y),
				"-video_size", fmt.Sprintf("%dx%d", even(r.W), even(r.H)), "-i", "desktop")
		}
	case "linux":
		display := p.Display
		if display == "" {
			display = ":0"
		}
		r := p.Capture
		args = append(args, "-thread_queue_size", "1024", "-f", "x11grab", "-framerate", fps, "-draw_mouse", "1",
			"-video_size", fmt.Sprintf("%dx%d", even(r.W), even(r.H)),
			"-i", fmt.Sprintf("%s+%d,%d", display, r.X, r.Y))
	default:
		return nil, fmt.Errorf("screen recording is not supported on %s", p.GOOS)
	}

	for _, a := range p.Audio {
		args = append(args, "-thread_queue_size", "1024", "-f", a.Format, "-i", a.Input)
	}

	var graph []string
	video := "[0:v]"
	if p.GOOS == "darwin" && p.cropped() {
		m, c := p.Monitor, p.Capture
		video += fmt.Sprintf("crop=w='trunc(iw*%d/%d/2)*2':h='trunc(ih*%d/%d/2)*2':x='iw*%d/%d':y='ih*%d/%d',",
			c.W, m.W, c.H, m.H, c.X-m.X, m.W, c.Y-m.Y, m.H)
	}
	// libx264 + yuv420p needs even dimensions.
	video += "scale=trunc(iw/2)*2:trunc(ih/2)*2,format=yuv420p[v]"
	graph = append(graph, video)
	audioMap := ""
	switch len(p.Audio) {
	case 1:
		audioMap = "1:a"
	case 2:
		graph = append(graph, "[1:a][2:a]amix=inputs=2:duration=longest:normalize=0[a]")
		audioMap = "[a]"
	}
	args = append(args, "-filter_complex", strings.Join(graph, ";"), "-map", "[v]")
	if audioMap != "" {
		args = append(args, "-map", audioMap, "-c:a", "aac", "-b:a", "160k")
	}
	args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "23")
	if p.MaxSec > 0 {
		args = append(args, "-t", strconv.FormatFloat(p.MaxSec, 'f', -1, 64))
	}
	return append(args, p.Out), nil
}

func even(n int) int { return n &^ 1 }

// avfDevice is one avfoundation device from `-list_devices true`.
type avfDevice struct {
	Index int    `json:"Index"`
	Name  string `json:"Name"`
}

var avfLine = regexp.MustCompile(`\]\s*\[(\d+)\]\s*(.+?)\s*$`)

// parseAVFDevices reads ffmpeg's avfoundation listing into video and audio devices.
func parseAVFDevices(out string) (video, audio []avfDevice) {
	section := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "video devices:"):
			section = "video"
			continue
		case strings.Contains(line, "audio devices:"):
			section = "audio"
			continue
		}
		m := avfLine.FindStringSubmatch(line)
		if m == nil || section == "" {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		d := avfDevice{Index: n, Name: m[2]}
		if section == "video" {
			video = append(video, d)
		} else {
			audio = append(audio, d)
		}
	}
	return video, audio
}

// avfScreenNumber guesses which "Capture screen N" shows a monitor.
// avfoundation numbers screens in NSScreen order, which starts with the main
// (primary) display; aos numbers monitors by position. So the primary is
// screen 0 and the rest follow in aos order. The caller verifies the guess
// against the real frame size, since the tail of that order is not promised.
func avfScreenNumber(monitors []engine.Monitor, id int) int {
	n := 1
	for _, m := range monitors {
		if m.Primary {
			if m.ID == id {
				return 0
			}
			continue
		}
		if m.ID == id {
			return n
		}
		n++
	}
	return id - 1
}

// aspectMatches reports whether a frame size has the monitor's shape.
func aspectMatches(w, h int, m engine.Rect) bool {
	if w <= 0 || h <= 0 || m.W <= 0 || m.H <= 0 {
		return false
	}
	a, b := float64(w)/float64(h), float64(m.W)/float64(m.H)
	return a/b > 0.98 && a/b < 1.02
}

// avfScreenIndex maps "Capture screen <screen>" onto its avfoundation device index.
func avfScreenIndex(video []avfDevice, screen int) (string, error) {
	want := fmt.Sprintf("Capture screen %d", screen)
	var screens []string
	for _, d := range video {
		if strings.HasPrefix(d.Name, "Capture screen") {
			screens = append(screens, d.Name)
			if d.Name == want {
				return strconv.Itoa(d.Index), nil
			}
		}
	}
	if len(screens) == 0 {
		return "", fmt.Errorf("ffmpeg lists no screens to record; grant Screen Recording to your terminal (`aos permission request screen`)")
	}
	return "", fmt.Errorf("no avfoundation device for Capture screen %d (have %s)", screen, strings.Join(screens, ", "))
}

// loopbackNames are the virtual devices that carry system audio on macOS.
var loopbackNames = []string{"blackhole", "loopback", "aos-loopback", "soundflower"}

func isLoopback(name string) bool {
	low := strings.ToLower(name)
	for _, n := range loopbackNames {
		if strings.Contains(low, n) {
			return true
		}
	}
	return false
}

// pickAudioDevice chooses a device by --mic (index or case-insensitive name
// part). With no choice it takes the first that is (or is not) a loopback.
func pickAudioDevice(devices []avfDevice, choice string, loopback bool) (avfDevice, bool) {
	if choice != "" {
		if n, err := strconv.Atoi(choice); err == nil {
			for _, d := range devices {
				if d.Index == n {
					return d, true
				}
			}
			return avfDevice{}, false
		}
		for _, d := range devices {
			if strings.Contains(strings.ToLower(d.Name), strings.ToLower(choice)) {
				return d, true
			}
		}
		return avfDevice{}, false
	}
	for _, d := range devices {
		if isLoopback(d.Name) == loopback {
			return d, true
		}
	}
	return avfDevice{}, false
}

// errNoLoopback is the macOS "system audio" refusal: ffmpeg cannot tap system
// output without a virtual loopback device, and aos never installs one itself.
const noLoopbackHint = "no loopback audio device found for --audio=system; install one yourself (e.g. `brew install blackhole-2ch`), route output to it, then retry"

// darwinAudio resolves --audio on macOS into avfoundation inputs.
func darwinAudio(mode audioMode, mic string, devices []avfDevice) ([]audioInput, error) {
	var out []audioInput
	if mode == audioMic || mode == audioBoth {
		d, ok := pickAudioDevice(devices, mic, false)
		if !ok {
			return nil, fmt.Errorf("no microphone matching %q; see `aos capture video devices`", mic)
		}
		out = append(out, audioInput{"avfoundation", "none:" + strconv.Itoa(d.Index)})
	}
	if mode == audioSystem || mode == audioBoth {
		d, ok := pickAudioDevice(devices, "", true)
		if !ok {
			return nil, &missingToolError{noLoopbackHint}
		}
		out = append(out, audioInput{"avfoundation", "none:" + strconv.Itoa(d.Index)})
	}
	return out, nil
}

var dshowLine = regexp.MustCompile(`"([^"]+)"\s*\((audio|video)\)`)

// parseDshowDevices reads `ffmpeg -list_devices true -f dshow -i dummy`.
func parseDshowDevices(out string) (video, audio []string) {
	for _, line := range strings.Split(out, "\n") {
		m := dshowLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if m[2] == "audio" {
			audio = append(audio, m[1])
		} else {
			video = append(video, m[1])
		}
	}
	return video, audio
}

// windowsAudio resolves --audio on Windows. System audio uses ffmpeg's wasapi
// loopback when this build has it, else a dshow loopback device when present.
func windowsAudio(mode audioMode, mic string, dshowAudio []string, hasWasapi bool) ([]audioInput, error) {
	var out []audioInput
	if mode == audioMic || mode == audioBoth {
		name := ""
		for _, d := range dshowAudio {
			if mic == "" && !isWindowsLoopback(d) || mic != "" && strings.Contains(strings.ToLower(d), strings.ToLower(mic)) {
				name = d
				break
			}
		}
		if name == "" {
			return nil, fmt.Errorf("no microphone matching %q; see `aos capture video devices`", mic)
		}
		out = append(out, audioInput{"dshow", "audio=" + name})
	}
	if mode == audioSystem || mode == audioBoth {
		switch {
		case hasWasapi:
			out = append(out, audioInput{"wasapi", "loopback"})
		default:
			name := ""
			for _, d := range dshowAudio {
				if isWindowsLoopback(d) {
					name = d
					break
				}
			}
			if name == "" {
				return nil, &missingToolError{"no system-audio source for --audio=system: enable \"Stereo Mix\" in Sound settings or install virtual-audio-capturer, then retry"}
			}
			out = append(out, audioInput{"dshow", "audio=" + name})
		}
	}
	return out, nil
}

func isWindowsLoopback(name string) bool {
	low := strings.ToLower(name)
	return strings.Contains(low, "stereo mix") || strings.Contains(low, "virtual-audio-capturer") || strings.Contains(low, "loopback")
}

// linuxAudio resolves --audio on Linux through PulseAudio / PipeWire-pulse:
// the mic is a source, system audio is the default sink's monitor.
func linuxAudio(mode audioMode, mic, defaultSink string) []audioInput {
	var out []audioInput
	if mode == audioMic || mode == audioBoth {
		src := mic
		if src == "" {
			src = "default"
		}
		out = append(out, audioInput{"pulse", src})
	}
	if mode == audioSystem || mode == audioBoth {
		src := "default.monitor"
		if defaultSink != "" {
			src = defaultSink + ".monitor"
		}
		out = append(out, audioInput{"pulse", src})
	}
	return out
}

// missingToolError is a setup problem the user fixes outside aos; it exits 2.
type missingToolError struct{ msg string }

func (e *missingToolError) Error() string { return e.msg }

// ffmpegInstallHint says how to get ffmpeg on each OS.
func ffmpegInstallHint(goos string) string {
	switch goos {
	case "darwin":
		return "brew install ffmpeg"
	case "windows":
		return "winget install Gyan.FFmpeg"
	}
	return "sudo apt install ffmpeg"
}
