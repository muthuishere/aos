package groups

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/windowctl"
)

func TestParseVideoStartModes(t *testing.T) {
	cases := []struct {
		args   []string
		mode   string
		follow string
	}{
		{nil, "monitor", ""},
		{[]string{"fullscreen"}, "monitor", ""},
		{[]string{"smart"}, "smart", "smart"},
		{[]string{"mouse", "--zoom=3"}, "mouse", "mouse"},
		{[]string{"app", "--app=Chrome"}, "app", "window"},
		{[]string{"window", "--title=README"}, "window", ""},
		{[]string{"region", "--region=0,0,800,600"}, "region", ""},
	}
	for _, tc := range cases {
		o, err := parseVideoStart(tc.args)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if o.Mode != tc.mode || o.Follow != tc.follow {
			t.Errorf("%v: got mode %q follow %q, want %q %q", tc.args, o.Mode, o.Follow, tc.mode, tc.follow)
		}
	}
}

func TestParseVideoStartFlags(t *testing.T) {
	o, err := parseVideoStart([]string{"smart", "--fps=24", "--audio=both", "--mic=USB", "--max=90s", "--indicator=false", "--out=/tmp/x.mp4", "--monitor=2", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if o.FPS != 24 || o.Audio != audioBoth || o.Mic != "USB" || o.Max != 90*time.Second || o.Indicator || o.Out != "/tmp/x.mp4" || *o.Monitor != 2 || !o.JSON {
		t.Errorf("unexpected %+v", o)
	}
	d, _ := parseVideoStart(nil)
	if d.Max != 10*time.Minute || !d.Indicator || d.Audio != audioNone || d.FPS != 30 || d.Zoom != 2 {
		t.Errorf("defaults wrong: %+v", d)
	}
}

func TestParseVideoStartRejects(t *testing.T) {
	bad := [][]string{
		{"window"},                        // needs --app
		{"app"},                           // needs --app
		{"region"},                        // needs --region
		{"smart", "--app=Chrome"},         // app goes with window/app
		{"monitor", "--region=0,0,10,10"}, // region goes with region
		{"--audio=loud"},                  // bad enum
		{"--mic=USB"},                     // mic without audio
		{"--fps=0"},                       //
		{"--max=soon"},                    //
		{"--zoom=0.5"},                    //
		{"tv"},                            // unknown mode
		{"monitor", "window"},             // two modes
		{"--follow-mouse"},                // dropped flag
	}
	for _, args := range bad {
		if _, err := parseVideoStart(args); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
}

func TestParseVideoDuration(t *testing.T) {
	for raw, want := range map[string]time.Duration{"90": 90 * time.Second, "1.5": 1500 * time.Millisecond, "10m": 10 * time.Minute, "1m30s": 90 * time.Second} {
		got, err := parseVideoDuration(raw)
		if err != nil || got != want {
			t.Errorf("%q: got %v %v", raw, got, err)
		}
	}
}

func TestParseVideoFocus(t *testing.T) {
	f, err := parseVideoFocus([]string{"--at=1000,500", "--size=800x400", "--hold=3s", "--zoom=2"})
	if err != nil {
		t.Fatal(err)
	}
	if *f.Rect != (windowctl.Rect{X: 600, Y: 300, W: 800, H: 400}) || f.Hold != 3 || f.Zoom != 2 {
		t.Errorf("got %+v %+v", f, *f.Rect)
	}
	f, err = parseVideoFocus([]string{"--app=Chrome", "--find=Sign in", "--role=button"})
	if err != nil || f.Match.App != "Chrome" || f.Find.Text != "Sign in" || f.Find.Role != "button" {
		t.Errorf("find spec: %+v %v", f, err)
	}
	for _, bad := range [][]string{nil, {"--rect=0,0,1,1", "--at=1,1"}, {"--find=x"}, {"--size=10x10", "--rect=0,0,5,5"}, {"--rect=0,0,5,5", "--hold=x"}} {
		if _, err := parseVideoFocus(bad); err == nil {
			t.Errorf("%v: expected an error", bad)
		}
	}
}

func TestFFmpegArgsDarwinCropsAsFractions(t *testing.T) {
	p := ffPlan{GOOS: "darwin", FPS: 30, MaxSec: 600, Out: "/tmp/o.mp4", DarwinScreen: "3",
		Monitor: windowctl.Rect{X: 0, Y: 0, W: 1512, H: 982}, Capture: windowctl.Rect{X: 100, Y: 50, W: 800, H: 600},
		Audio: []audioInput{{"avfoundation", "none:3"}}}
	args, err := ffmpegRecordArgs(p)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Join(args, " ")
	for _, want := range []string{"-f avfoundation", "-capture_cursor 1", "-i 3:none", "-i none:3",
		"crop=w='trunc(iw*800/1512/2)*2'", "x='iw*100/1512'", "y='ih*50/982'", "-map 1:a", "-t 600", "/tmp/o.mp4"} {
		if !strings.Contains(line, want) {
			t.Errorf("missing %q in %s", want, line)
		}
	}
	p.Capture = p.Monitor
	p.Audio = nil
	args, _ = ffmpegRecordArgs(p)
	if line := strings.Join(args, " "); strings.Contains(line, "crop=") || strings.Contains(line, "-c:a") {
		t.Errorf("full monitor should not crop or carry audio: %s", line)
	}
}

func TestFFmpegArgsWindowsAndLinux(t *testing.T) {
	r := windowctl.Rect{X: -1920, Y: 0, W: 1281, H: 721}
	w, _ := ffmpegRecordArgs(ffPlan{GOOS: "windows", FPS: 15, Out: "o.mp4", Monitor: r, Capture: r,
		Audio: []audioInput{{"dshow", "audio=Mic"}, {"wasapi", "loopback"}}})
	wl := strings.Join(w, " ")
	for _, want := range []string{"-f gdigrab", "-offset_x -1920", "-video_size 1280x720", "-i desktop", "amix=inputs=2", "-map [a]"} {
		if !strings.Contains(wl, want) {
			t.Errorf("windows: missing %q in %s", want, wl)
		}
	}
	w, _ = ffmpegRecordArgs(ffPlan{GOOS: "windows", Out: "o.mp4", WinTitle: "Notepad", Monitor: r, Capture: r})
	if !strings.Contains(strings.Join(w, " "), "-i title=Notepad") {
		t.Errorf("windows title capture: %v", w)
	}
	l, _ := ffmpegRecordArgs(ffPlan{GOOS: "linux", Out: "o.mp4", Display: ":1", Monitor: r, Capture: windowctl.Rect{X: 10, Y: 20, W: 640, H: 480}})
	if ll := strings.Join(l, " "); !strings.Contains(ll, "-f x11grab") || !strings.Contains(ll, "-i :1+10,20") || !strings.Contains(ll, "-video_size 640x480") {
		t.Errorf("linux: %s", ll)
	}
	if _, err := ffmpegRecordArgs(ffPlan{GOOS: "plan9"}); err == nil {
		t.Error("unknown OS should fail")
	}
}

const avfListing = `[AVFoundation indev @ 0x1] AVFoundation video devices:
[AVFoundation indev @ 0x1] [0] MacBook Pro Camera
[AVFoundation indev @ 0x1] [3] Capture screen 0
[AVFoundation indev @ 0x1] [4] Capture screen 1
[AVFoundation indev @ 0x1] AVFoundation audio devices:
[AVFoundation indev @ 0x1] [0] BlackHole 2ch
[AVFoundation indev @ 0x1] [1] MacBook Pro Microphone
`

func TestAVFoundationDevicesAndAudio(t *testing.T) {
	video, audio := parseAVFDevices(avfListing)
	if len(video) != 3 || len(audio) != 2 {
		t.Fatalf("got %v %v", video, audio)
	}
	if idx, err := avfScreenIndex(video, 1); err != nil || idx != "4" {
		t.Errorf("screen 1 -> %q %v", idx, err)
	}
	if _, err := avfScreenIndex(video, 5); err == nil {
		t.Error("monitor 5 should not exist")
	}
	in, err := darwinAudio(audioBoth, "", audio)
	if err != nil || len(in) != 2 || in[0].Input != "none:1" || in[1].Input != "none:0" {
		t.Errorf("both: %v %v", in, err)
	}
	if in, _ := darwinAudio(audioMic, "blackhole", audio); in[0].Input != "none:0" {
		t.Errorf("--mic by name: %v", in)
	}
	_, err = darwinAudio(audioSystem, "", audio[1:])
	if _, ok := err.(*missingToolError); !ok || !strings.Contains(err.Error(), "blackhole-2ch") {
		t.Errorf("no loopback should be a setup error with a hint, got %v", err)
	}
}

func TestAVFScreenNumberPutsPrimaryFirst(t *testing.T) {
	monitors := []windowctl.Monitor{{ID: 1, X: -1728}, {ID: 2, X: 0, Primary: true}, {ID: 3, X: 1920}}
	for id, want := range map[int]int{2: 0, 1: 1, 3: 2} {
		if got := avfScreenNumber(monitors, id); got != want {
			t.Errorf("monitor %d -> screen %d, want %d", id, got, want)
		}
	}
	if !aspectMatches(3456, 2234, windowctl.Rect{W: 1728, H: 1117}) || aspectMatches(1920, 1080, windowctl.Rect{W: 1728, H: 1117}) {
		t.Error("aspect check")
	}
}

func TestDshowAndLinuxAudio(t *testing.T) {
	_, audio := parseDshowDevices(`[dshow @ 0] "Integrated Camera" (video)
[dshow @ 0] "Microphone (Realtek)" (audio)
[dshow @ 0] "Stereo Mix (Realtek)" (audio)`)
	if len(audio) != 2 {
		t.Fatalf("got %v", audio)
	}
	in, err := windowsAudio(audioBoth, "", audio, false)
	if err != nil || in[0].Input != "audio=Microphone (Realtek)" || in[1].Input != "audio=Stereo Mix (Realtek)" {
		t.Errorf("dshow both: %v %v", in, err)
	}
	if in, _ := windowsAudio(audioSystem, "", nil, true); in[0].Format != "wasapi" {
		t.Errorf("wasapi preferred: %v", in)
	}
	if _, err := windowsAudio(audioSystem, "", audio[:1], false); err == nil {
		t.Error("no loopback on windows should fail")
	}
	l := linuxAudio(audioBoth, "", "alsa_output.pci")
	if l[0].Input != "default" || l[1].Input != "alsa_output.pci.monitor" {
		t.Errorf("linux: %v", l)
	}
}

func TestEventCoordinatesRetina(t *testing.T) {
	// A 1512x982 pt monitor at x=1920 recorded at 2x into 3024x1964 pixels.
	capture := windowctl.Rect{X: 1920, Y: 0, W: 1512, H: 982}
	g := newCamGeom(1920, 1080, 3024, 1964, capture)
	v := g.videoRect(windowctl.Rect{X: 1920 + 100, Y: 50, W: 200, H: 100})
	if v != (frect{200, 100, 400, 200}) {
		t.Errorf("video rect %+v", v)
	}
	c := g.canvasRect(windowctl.Rect{X: 1920, Y: 0, W: 1512, H: 982})
	if math.Abs(c.H-1080) > 0.01 || math.Abs(c.X+c.W/2-960) > 0.01 {
		t.Errorf("full capture should fill the canvas height, centred: %+v", c)
	}
	// Unknown capture: rects are already video pixels.
	g2 := newCamGeom(1920, 1080, 1920, 1080, windowctl.Rect{})
	if r := g2.canvasRect(windowctl.Rect{X: 10, Y: 20, W: 30, H: 40}); r != (frect{10, 20, 30, 40}) {
		t.Errorf("identity %+v", r)
	}
}

func TestFitAndClamp(t *testing.T) {
	g := newCamGeom(1920, 1080, 1920, 1080, windowctl.Rect{})
	c := g.fit(frect{0, 0, 100, 100}, 0.15, 2.5)
	if c.Z != 2.5 {
		t.Errorf("small target should cap at max zoom, got %v", c.Z)
	}
	if c.CX != 1920/5.0 || c.CY != 1080/5.0 {
		t.Errorf("corner target should be clamped inside: %+v", c)
	}
	if w := g.fit(frect{0, 0, 1920, 1080}, 0.15, 2.5); w.Z != 1 {
		t.Errorf("whole-frame target should be wide: %+v", w)
	}
	mid := g.fit(frect{760, 440, 400, 200}, 0, 10)
	if math.Abs(mid.Z-4.8) > 1e-9 || mid.CX != 960 || mid.CY != 540 {
		t.Errorf("16:9 fit: %+v", mid)
	}
}

func TestSpringAndDeadZone(t *testing.T) {
	x, v := 0.0, 0.0
	omega := 2.15 / 0.3
	prev := x
	for i := 0; i < 60; i++ {
		x, v = springStep(x, v, 100, omega, 1.0/30)
		if x < prev-1e-9 || x > 100+1e-9 {
			t.Fatalf("critically damped spring must rise monotonically without overshoot: %v at step %d", x, i)
		}
		prev = x
	}
	if math.Abs(x-100) > 0.5 {
		t.Errorf("spring should settle within 2s, at %v", x)
	}
	x, _ = springStep(0, 0, 100, omega, 0.3)
	if x < 55 || x > 70 {
		t.Errorf("0.3s lag should cover about 63%%, got %v", x)
	}
	held := cam{100, 100, 2}
	if deadZone(held, cam{105, 103, 2}, 20) != held {
		t.Error("jitter inside the dead zone must not move the target")
	}
	if deadZone(held, cam{150, 100, 2}, 20) == held {
		t.Error("a real move must pass the dead zone")
	}
	if k := clickPulse(0.25, []float64{0}); math.Abs(k-1.1) > 1e-9 {
		t.Errorf("pulse peak %v", k)
	}
	if clickPulse(1, []float64{0}) != 1 {
		t.Error("pulse must end")
	}
}

func evRect(t float64, kind, cmd string, r windowctl.Rect) videoEvent {
	return videoEvent{T: t, Kind: kind, Cmd: cmd, Rect: &r}
}

func camAt(keys []camKey, t float64) cam {
	best := keys[0]
	for _, k := range keys {
		if k.T <= t+1e-9 {
			best = k
		}
	}
	return best.C
}

func TestPlanCameraActionEvents(t *testing.T) {
	g := newCamGeom(1920, 1080, 1920, 1080, windowctl.Rect{X: 0, Y: 0, W: 1920, H: 1080})
	doc := videoDoc{Events: []videoEvent{
		evRect(3, "action", "find", windowctl.Rect{X: 100, Y: 100, W: 200, H: 100}),
		evRect(4, "action", "draw", windowctl.Rect{X: 1500, Y: 800, W: 200, H: 100}),
		evRect(12, "action", "window focus", windowctl.Rect{X: 800, Y: 400, W: 300, H: 200}),
	}}
	keys := planCamera(doc, g, zoomOptions{}, 20)
	if c := camAt(keys, 0.5); c.Z != 1 {
		t.Errorf("wide at start, got %+v", c)
	}
	if c := camAt(keys, 3.2); c.Z < 2 || c.CX > 700 {
		t.Errorf("zoomed on the first event at t=3.2: %+v", c)
	}
	// Events 1s apart chain directly: the camera never returns to wide between.
	for t0 := 3.0; t0 < 4.5; t0 += 1.0 / 15 {
		if camAt(keys, t0).Z < 1.5 {
			t.Fatalf("went wide between chained events at %v", t0)
		}
	}
	if c := camAt(keys, 9); c.Z != 1 {
		t.Errorf("back to wide in the >3s gap: %+v", c)
	}
	if c := camAt(keys, 19.9); c.Z != 1 {
		t.Errorf("wide at the end: %+v", c)
	}
	// Deterministic.
	again := planCamera(doc, g, zoomOptions{}, 20)
	if len(again) != len(keys) || again[len(again)/2] != keys[len(keys)/2] {
		t.Error("planning must be deterministic")
	}
}

func TestPlanCameraFocusBeatsActionsAndFollow(t *testing.T) {
	g := newCamGeom(1920, 1080, 1920, 1080, windowctl.Rect{W: 1920, H: 1080})
	focus := evRect(2, "focus", "capture video focus", windowctl.Rect{X: 1600, Y: 900, W: 200, H: 100})
	doc := videoDoc{Follow: "mouse", Events: []videoEvent{
		{T: 0.1, Kind: "cursor", At: &windowctl.Point{X: 200, Y: 200}},
		focus,
		evRect(3, "action", "draw", windowctl.Rect{X: 0, Y: 0, W: 100, H: 100}),
		{T: 6, Kind: "unfocus"},
	}}
	keys := planCamera(doc, g, zoomOptions{}, 10)
	c := camAt(keys, 5)
	if c.CX < 1400 || c.CY < 700 {
		t.Errorf("explicit focus must win over the draw event and the cursor: %+v", c)
	}
	c = camAt(keys, 9.5)
	if math.Abs(c.CX-480) > 1 || math.Abs(c.CY-270) > 1 || math.Abs(c.Z-2) > 0.05 { // cursor at 200,200 clamped to the frame edge
		t.Errorf("after unfocus the mouse follow resumes: %+v", c)
	}
}

func TestSmartModeLeansToCursorThenWindow(t *testing.T) {
	g := newCamGeom(1920, 1080, 1920, 1080, windowctl.Rect{W: 1920, H: 1080})
	win := windowctl.Rect{X: 0, Y: 0, W: 1920, H: 1000}
	doc := videoDoc{Follow: "smart", Events: []videoEvent{evRect(0, "window", "", win)}}
	for i := 0; i < 20; i++ {
		x := 300 + i*5
		doc.Events = append(doc.Events, videoEvent{T: 1 + float64(i)*0.05, Kind: "cursor", At: &windowctl.Point{X: x, Y: 300}})
	}
	keys := planCamera(doc, g, zoomOptions{}, 8)
	if c := camAt(keys, 2.3); c.Z < 1.6 {
		t.Errorf("a large window with an active cursor should lean in: %+v", c)
	}
	if c := camAt(keys, 7.5); c.Z > 1.2 {
		t.Errorf("an idle cursor should hand back to the window: %+v", c)
	}
	// A draw takes priority in smart mode; a plain key press does not.
	if !isCameraAction(evRect(1, "action", "draw", win), "smart") || isCameraAction(evRect(1, "action", "key type", win), "smart") {
		t.Error("smart-mode action filter")
	}
	if isCameraAction(evRect(1, "action", "find", win), "mouse") {
		t.Error("follow-mouse ignores actions")
	}
}

func TestDrawEventIsUnionOfSignAndTarget(t *testing.T) {
	u := unionRect(windowctl.Rect{X: 100, Y: 100, W: 50, H: 50}, windowctl.Rect{X: 300, Y: 20, W: 10, H: 10})
	if u != (windowctl.Rect{X: 100, Y: 20, W: 210, H: 130}) {
		t.Errorf("union %+v", u)
	}
}

func TestSimplifyAndExpressions(t *testing.T) {
	var keys []camKey
	for i := 0; i <= 30; i++ {
		t0 := float64(i) / 10
		keys = append(keys, camKey{t0, cam{960 + 100*t0, 540, 1 + t0/3}})
	}
	s := simplifyKeys(keys, 0.5)
	if len(s) != 2 {
		t.Fatalf("a straight line should collapse to its ends, got %d keys", len(s))
	}
	expr := keyExpr(s, func(c cam) float64 { return c.CX })
	if expr != "gte(t,0)*lt(t,3)*(960+100*(t-0))+gte(t,3)*1260" {
		t.Errorf("expr %s", expr)
	}
	if keyExpr([]camKey{{0, cam{1, 2, 1}}}, func(c cam) float64 { return c.CY }) != "2" {
		t.Error("single key is a constant")
	}
	g := newCamGeom(1920, 1080, 3024, 1964, windowctl.Rect{})
	f := cameraFilter(s, g, []frect{{10, 10, 44, 44}}, []float64{1})
	for _, want := range []string{"force_original_aspect_ratio=decrease", "pad=1920:1080", "drawbox=", "eval=frame", "crop=w=1920:h=1080", "[v]"} {
		if !strings.Contains(f, want) {
			t.Errorf("filter missing %q: %s", want, f)
		}
	}
	args := strings.Join(renderArgs("in.mp4", "s.txt", "out.mp4", true), " ")
	if !strings.Contains(args, "-filter_complex_script s.txt") || !strings.Contains(args, "-map 0:a -c:a copy") {
		t.Errorf("render args %s", args)
	}
}

func TestVideoStateAndEventsFiles(t *testing.T) {
	dir := t.TempDir()
	if st, err := readVideoState(dir); st != nil || err != nil {
		t.Fatalf("no state yet: %v %v", st, err)
	}
	out := filepath.Join(dir, "demo.mp4")
	st := &videoState{PID: 42, Out: out, Events: filepath.Join(dir, "demo.events.jsonl"), Started: time.Now().Add(-5 * time.Second),
		Mode: "smart", Follow: "smart", Capture: windowctl.Rect{W: 100, H: 100}}
	if err := writeVideoState(dir, st); err != nil {
		t.Fatal(err)
	}
	got, err := readVideoState(dir)
	if err != nil || got.PID != 42 || got.Mode != "smart" {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	r := windowctl.Rect{X: 1, Y: 2, W: 3, H: 4}
	for i := 0; i < 3; i++ {
		if err := appendVideoEvent(st.Events, videoEvent{T: float64(i), Kind: "mark", Rect: &r, Label: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	evs, err := readVideoEvents(st.Events)
	if err != nil || len(evs) != 3 || *evs[2].Rect != r {
		t.Fatalf("events: %v %v", evs, err)
	}
	// No ffprobe-able video here; finishing still writes the doc and clears state.
	doc, err := finishVideo(got, dir)
	if err != nil || len(doc.Events) != 3 {
		t.Fatalf("finish: %v %v", doc, err)
	}
	if _, err := os.Stat(eventsJSONPath(out)); err != nil {
		t.Error("events.json not written")
	}
	if st, _ := readVideoState(dir); st != nil {
		t.Error("state should be cleared")
	}
	if eventsJSONPath("/a/b.mp4") != "/a/b.events.json" || finalPath("/a/b.mp4") != "/a/b.final.mp4" {
		t.Error("derived paths")
	}
	if videoOffset(10.6, 10) != 0.6 || videoOffset(9, 10) != 0 {
		t.Error("offset")
	}
}

func TestParseProbe(t *testing.T) {
	info, err := parseProbe([]byte(`{"streams":[{"codec_type":"video","width":3024,"height":1964},{"codec_type":"audio"}],"format":{"duration":"3.2"}}`))
	if err != nil || info.W != 3024 || info.H != 1964 || !info.Audio || info.Duration != 3.2 {
		t.Errorf("%+v %v", info, err)
	}
}
