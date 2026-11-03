package groups

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// `aos say` speaks text with the machine's own free voices: `say` on macOS,
// SAPI 5 over COM on Windows (the OneCore voices first, then the desktop
// ones), called from Go with no PowerShell. Every utterance is rendered to an audio file first and then
// played, so the same bytes the person hears can be dropped into a recording's
// narration track at the moment they were spoken.
//
// Speakers queue: a second `say` (async or not) waits for the first to finish,
// so narration lines never talk over each other. State lives under
// <config>/say: lock (the PID speaking now) and q/<pid>.json (every speaker
// alive, queued or speaking), which `say wait` and `say stop` read.

// sayVoice is one installed voice.
type sayVoice struct {
	Name     string `json:"Name"`
	Language string `json:"Language"`
	Gender   string `json:"Gender,omitempty"`
	Engine   string `json:"Engine"` // say | onecore | sapi
}

// sayRequest is one utterance to render.
type sayRequest struct {
	Text  string
	Voice string
	Rate  int // words per minute; 0 = the voice's default
}

// sayDefaultWPM is the rate macOS and Windows both treat as "normal".
const sayDefaultWPM = 175

var sayValueFlags = []string{"voice", "rate", "out", "then-wait"}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("say", "Speak text aloud with the machine's built-in voices; narrate recordings")
		plat := []string{"darwin", "windows"}
		r.Add(
			&cli.Command{
				Group: "say", Name: "",
				Summary: "Speak text aloud (or render it to an mp3/wav/aiff); --async returns at once",
				Args:    "<text...> [--voice=<name>] [--rate=<words per minute>] [--out=<file.mp3|.wav|.aiff>] [--no-subtitle] [--async] [--narrate] [--json] [-- <command...> [--then-wait=false]]",
				Examples: []string{
					`aos say "Now I open Notepad and type the note" -- aos key type --app=Notepad "hello"   # speak AND act, wait for both`,
					`aos say "build finished"`,
					`aos say "Hello there" --voice=Samantha --rate=190`,
					`aos say "welcome to the demo" --out=~/Desktop/welcome.mp3`,
					`aos say "now I open the settings" --async   # keeps talking while the script goes on`,
					"aos say wait",
					"# while `aos capture video` runs, every say is added to <out>.final.mp4 at the moment it was spoken",
					"# --narrate adds it to the recording's narration track without playing it aloud",
					"# the text is shown as a subtitle while it is spoken, cleared when it ends; --no-subtitle turns that off",
				},
				Platforms: plat,
				Run:       runSay,
			},
			&cli.Command{
				Group: "say", Name: "voices",
				Summary:   "List the installed voices",
				Args:      "[--json]",
				Examples:  []string{"aos say voices", "aos say voices --json"},
				Platforms: plat,
				Run:       runSayVoices,
			},
			&cli.Command{
				Group: "say", Name: "wait",
				Summary:   "Block until every queued and playing `say --async` has finished",
				Args:      "[--timeout=<seconds>]",
				Examples:  []string{"aos say wait", "aos say wait --timeout=30"},
				Platforms: plat,
				Run:       runSayWait,
			},
			&cli.Command{
				Group: "say", Name: "stop",
				Summary:   "Stop speaking now and drop anything queued",
				Examples:  []string{"aos say stop"},
				Platforms: plat,
				Run:       runSayStop,
			},
		)
	})
}

// ---- parsing ---------------------------------------------------------------

type sayOptions struct {
	req     sayRequest
	out     string
	async   bool
	narrate bool
	caption bool // show the text as a subtitle while it is spoken
	json    bool
}

func parseSay(args []string) (sayOptions, error) {
	var o sayOptions
	set, err := parseArgs(args, sayValueFlags...)
	if err != nil {
		return o, err
	}
	if err := set.Reject(append([]string{"async", "narrate", "json", "no-subtitle"}, sayValueFlags...)...); err != nil {
		return o, err
	}
	o.req.Text = strings.TrimSpace(strings.Join(set.Rest, " "))
	if o.req.Text == "" {
		return o, fmt.Errorf("`say` needs some text, e.g. aos say \"hello\"")
	}
	o.req.Voice = set.String("voice", "")
	if o.req.Rate, err = set.Int("rate", 0); err != nil {
		return o, err
	}
	if o.req.Rate != 0 && (o.req.Rate < 50 || o.req.Rate > 700) {
		return o, fmt.Errorf("--rate is words per minute, 50..700 (normal is %d)", sayDefaultWPM)
	}
	if raw := set.String("out", ""); raw != "" {
		o.out = expandHome(raw)
		switch strings.ToLower(filepath.Ext(o.out)) {
		case ".mp3", ".wav", ".aiff", ".aif", ".m4a":
		default:
			return o, fmt.Errorf("--out must end in .mp3, .wav, .aiff or .m4a")
		}
	}
	o.async, o.narrate, o.json = set.Has("async"), set.Has("narrate"), set.Has("json")
	o.caption = !set.Has("no-subtitle")
	return o, nil
}

// ---- say -------------------------------------------------------------------

func runSay(c *cli.Ctx, args []string) error {
	if sayArgs, cmdArgs, ok := splitDashDash(args); ok {
		return runSayWhile(c, sayArgs, cmdArgs)
	}
	o, err := parseSay(args)
	if err != nil {
		return err
	}
	if o.async {
		return spawnSay(c, withoutFlag(args, "async"))
	}
	dir := sayDir(c)
	// A file-only render (--out with no recording) does not need the queue.
	rec := activeRecording(c)
	play := !o.narrate && o.out == ""
	if !play && rec == nil {
		return sayToFile(c, o)
	}
	if err := sayEnqueue(dir); err != nil {
		return err
	}
	defer sayDequeue(dir)

	if !play && o.out != "" { // --out during a recording: write the file, and narrate it too
		if err := sayRenderTo(o.req, o.out); err != nil {
			return err
		}
	}
	clip, err := sayRenderClip(c, dir, o.req, rec != nil)
	if err != nil {
		return err
	}
	if rec == nil {
		defer os.Remove(clip)
	}
	dur := audioDuration(clip)
	if rec != nil {
		_ = appendVideoEvent(rec.Events, videoEvent{T: since(rec.Started), Kind: "say", Label: clip, Hold: dur, Cmd: o.req.Text})
	}
	// Speech and subtitle go together: the same text is on screen for exactly
	// as long as it is heard (and so lands in a recording in sync with it).
	captioned := o.caption && (play || o.narrate) && showSayCaption(c, o.req.Text, dur)
	if captioned {
		defer cli.Invoke(c, []string{"subtitle", "stop"}, "")
	}
	if play {
		if err := playClip(dir, clip); err != nil {
			return err
		}
	} else if o.narrate {
		// keep the queue honest: the next line must not start inside this one.
		time.Sleep(time.Duration(dur * float64(time.Second)))
	}
	return sayReport(c, o, map[string]any{"Spoken": play, "Seconds": dur, "Narration": rec != nil, "Out": o.out})
}

// showSayCaption puts the spoken text up as a subtitle for the clip's length.
// A caption that cannot be shown (no display, say) never fails the speech.
func showSayCaption(c *cli.Ctx, text string, seconds float64) bool {
	secs := math.Max(1, math.Ceil(seconds*10)/10)
	res := cli.Invoke(c, []string{"subtitle", "show", text, "--seconds=" + strconv.FormatFloat(secs, 'f', 1, 64)}, "")
	if res.Exit != 0 {
		fmt.Fprintf(c.Stderr, "aos say: no subtitle (%s)\n", strings.TrimSpace(res.Stderr))
		return false
	}
	return true
}

func sayReport(c *cli.Ctx, o sayOptions, res map[string]any) error {
	if o.json {
		return json.NewEncoder(c.Stdout).Encode(res)
	}
	if out, _ := res["Out"].(string); out != "" {
		c.Printf("wrote %s\n", out)
	}
	return nil
}

// sayToFile renders to --out without playing anything.
func sayToFile(c *cli.Ctx, o sayOptions) error {
	if err := sayRenderTo(o.req, o.out); err != nil {
		return err
	}
	return sayReport(c, o, map[string]any{"Spoken": false, "Seconds": audioDuration(o.out), "Out": o.out})
}

// sayRenderClip renders to a native-format file: in the recording's narration
// folder when one runs (kept for the final mix), else a temp file.
func sayRenderClip(c *cli.Ctx, dir string, req sayRequest, keep bool) (string, error) {
	folder := os.TempDir()
	if keep {
		folder = filepath.Join(videoDir(c), "narration")
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(folder, fmt.Sprintf("say-%d-%d%s", time.Now().UnixMilli(), os.Getpid(), sayNativeExt))
	if err := sayRender(req, path); err != nil {
		return "", err
	}
	return path, nil
}

// sayRenderTo renders to any supported extension, converting with ffmpeg when
// the native format (aiff on macOS, wav on Windows) is not what was asked for.
func sayRenderTo(req sayRequest, out string) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(out))
	if ext == sayNativeExt || (ext == ".aif" && sayNativeExt == ".aiff") {
		return sayRender(req, out)
	}
	ff, err := ffmpegPath(runtime.GOOS)
	if err != nil {
		return err
	}
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("aos-say-%d%s", time.Now().UnixNano(), sayNativeExt))
	defer os.Remove(tmp)
	if err := sayRender(req, tmp); err != nil {
		return err
	}
	cmd := exec.Command(ff, convertArgs(tmp, out)...)
	hideChild(cmd)
	if msg, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(msg)))
	}
	return nil
}

func convertArgs(in, out string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", in}
	switch strings.ToLower(filepath.Ext(out)) {
	case ".mp3":
		args = append(args, "-c:a", "libmp3lame", "-q:a", "3")
	case ".m4a":
		args = append(args, "-c:a", "aac", "-b:a", "160k")
	case ".wav":
		args = append(args, "-c:a", "pcm_s16le")
	}
	return append(args, out)
}

// audioDuration is a clip's length in seconds (0 when ffprobe is missing).
func audioDuration(path string) float64 {
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path)
	hideChild(cmd)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	d, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return d
}

// spawnSay re-runs this `say` detached and returns at once. The child queues
// itself before the parent returns, so an immediate `say wait` sees it.
func spawnSay(c *cli.Ctx, args []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir := sayDir(c)
	if err := os.MkdirAll(filepath.Join(dir, "q"), 0o755); err != nil {
		return err
	}
	cmd := exec.Command(self, append([]string{"say"}, args...)...)
	cmd.Env = os.Environ()
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	pid := cmd.Process.Pid
	_ = writeSayEntry(dir, sayEntry{PID: pid, Started: time.Now()})
	_ = cmd.Process.Release()
	c.Printf("speaking in the background (pid %d); `aos say wait` blocks until done\n", pid)
	return nil
}

// ---- queue -----------------------------------------------------------------

type sayEntry struct {
	PID     int       `json:"PID"`
	Player  int       `json:"Player,omitempty"`
	Started time.Time `json:"Started"`
}

func sayDir(c *cli.Ctx) string { return filepath.Join(cli.ConfigDir(c.Env), "say") }

func sayEntryPath(dir string, pid int) string {
	return filepath.Join(dir, "q", strconv.Itoa(pid)+".json")
}

func writeSayEntry(dir string, e sayEntry) error {
	if err := os.MkdirAll(filepath.Join(dir, "q"), 0o755); err != nil {
		return err
	}
	data, _ := json.Marshal(e)
	return os.WriteFile(sayEntryPath(dir, e.PID), data, 0o644)
}

// sayEntries are the live speakers, oldest first; dead ones are swept.
func sayEntries(dir string) []sayEntry {
	files, _ := filepath.Glob(filepath.Join(dir, "q", "*.json"))
	var out []sayEntry
	for _, f := range files {
		var e sayEntry
		data, err := os.ReadFile(f)
		if err != nil || json.Unmarshal(data, &e) != nil || !videoProcAlive(e.PID) {
			_ = os.Remove(f)
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.Before(out[j].Started) })
	return out
}

func sayLockPath(dir string) string { return filepath.Join(dir, "lock") }

// sayEnqueue registers this process and waits for its turn to speak.
func sayEnqueue(dir string) error {
	me := os.Getpid()
	started := time.Now()
	if data, err := os.ReadFile(sayEntryPath(dir, me)); err == nil { // queued by spawnSay
		var e sayEntry
		if json.Unmarshal(data, &e) == nil {
			started = e.Started
		}
	}
	if err := writeSayEntry(dir, sayEntry{PID: me, Started: started}); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		// first in line among the living, and nobody holds the lock
		if q := sayEntries(dir); len(q) > 0 && q[0].PID == me {
			f, err := os.OpenFile(sayLockPath(dir), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err == nil {
				fmt.Fprint(f, me)
				f.Close()
				return nil
			}
			if raw, rerr := os.ReadFile(sayLockPath(dir)); rerr == nil {
				if pid, _ := strconv.Atoi(strings.TrimSpace(string(raw))); !videoProcAlive(pid) {
					_ = os.Remove(sayLockPath(dir))
					continue
				}
			}
		}
		time.Sleep(80 * time.Millisecond)
	}
	return errors.New("timed out waiting for another `say` to finish")
}

func sayDequeue(dir string) {
	me := os.Getpid()
	if raw, err := os.ReadFile(sayLockPath(dir)); err == nil && strings.TrimSpace(string(raw)) == strconv.Itoa(me) {
		_ = os.Remove(sayLockPath(dir))
	}
	_ = os.Remove(sayEntryPath(dir, me))
}

// recordPlayer notes the PID of an external player so `say stop` can silence
// it. Players that run inside this process need nothing recorded.
func recordPlayer(dir string, pid int) {
	me := os.Getpid()
	if data, err := os.ReadFile(sayEntryPath(dir, me)); err == nil {
		var e sayEntry
		if json.Unmarshal(data, &e) == nil {
			e.Player = pid
			_ = writeSayEntry(dir, e)
		}
	}
}

func runSayWait(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("timeout"); err != nil {
		return err
	}
	secs, err := set.Int("timeout", 600)
	if err != nil {
		return err
	}
	dir := sayDir(c)
	deadline := time.Now().Add(time.Duration(secs) * time.Second)
	for len(sayEntries(dir)) > 0 {
		if time.Now().After(deadline) {
			return &cli.ExitError{Code: 1, Message: "still speaking after the timeout"}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

func runSayStop(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`say stop` takes no arguments")
	}
	dir := sayDir(c)
	n := 0
	for _, e := range sayEntries(dir) {
		killTree(e.PID)
		if e.Player > 0 {
			killTree(e.Player)
		}
		_ = os.Remove(sayEntryPath(dir, e.PID))
		n++
	}
	_ = os.Remove(sayLockPath(dir))
	c.Printf("stopped %d speaker(s)\n", n)
	return nil
}

// ---- voices ----------------------------------------------------------------

func runSayVoices(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	voices, err := sayVoices()
	if err != nil {
		return err
	}
	if set.Has("json") {
		if voices == nil {
			voices = []sayVoice{}
		}
		return json.NewEncoder(c.Stdout).Encode(voices)
	}
	for _, v := range voices {
		c.Printf("%-28s %-8s %-7s %s\n", v.Name, v.Language, v.Gender, v.Engine)
	}
	return nil
}

// ---- recording mix ---------------------------------------------------------

// narrationClip is one spoken line placed on the video's timeline.
type narrationClip struct {
	Path string
	At   float64 // seconds into the video
}

// narrationClips picks the `say` events out of a finished recording.
func narrationClips(doc videoDoc) []narrationClip {
	var out []narrationClip
	for _, ev := range doc.Events {
		if ev.Kind != "say" || ev.Label == "" {
			continue
		}
		at := ev.T - doc.Offset
		if at < 0 {
			at = 0
		}
		if doc.Duration > 0 && at >= doc.Duration {
			continue
		}
		out = append(out, narrationClip{Path: ev.Label, At: at})
	}
	return out
}

// narrationMixArgs builds the ffmpeg call that lays clips over a video. With
// keepAudio the recording's own sound stays underneath; otherwise the
// narration replaces it.
func narrationMixArgs(video string, clips []narrationClip, keepAudio bool, duration float64, out string) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", video}
	for _, c := range clips {
		args = append(args, "-i", c.Path)
	}
	var f strings.Builder
	var labels []string
	if keepAudio {
		labels = append(labels, "[0:a]")
	}
	for i, c := range clips {
		ms := int(c.At*1000 + 0.5)
		fmt.Fprintf(&f, "[%d:a]aresample=48000,adelay=%d|%d[n%d];", i+1, ms, ms, i)
		labels = append(labels, fmt.Sprintf("[n%d]", i))
	}
	fmt.Fprintf(&f, "%samix=inputs=%d:duration=longest:dropout_transition=0:normalize=0[a]", strings.Join(labels, ""), len(labels))
	args = append(args, "-filter_complex", f.String(), "-map", "0:v", "-map", "[a]",
		"-c:v", "copy", "-c:a", "aac", "-b:a", "160k")
	if duration > 0 {
		args = append(args, "-t", strconv.FormatFloat(duration, 'f', 3, 64))
	}
	return append(args, "-movflags", "+faststart", out)
}

// mixNarration lays the recording's spoken lines into video in place.
// It returns how many lines were mixed.
func mixNarration(video string, doc videoDoc, keepAudio bool) (int, error) {
	clips := narrationClips(doc)
	if len(clips) == 0 {
		return 0, nil
	}
	info, err := probeVideo(video)
	if err != nil {
		return 0, err
	}
	tmp := strings.TrimSuffix(video, filepath.Ext(video)) + ".narrated.tmp.mp4"
	cmd := exec.Command("ffmpeg", narrationMixArgs(video, clips, keepAudio && info.Audio, info.Duration, tmp)...)
	hideChild(cmd)
	if msg, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(tmp)
		return 0, fmt.Errorf("ffmpeg: %v: %s", err, strings.TrimSpace(string(msg)))
	}
	if err := os.Rename(tmp, video); err != nil {
		return 0, err
	}
	for _, c := range clips {
		_ = os.Remove(c.Path)
	}
	return len(clips), nil
}

var sayVoiceLine = regexp.MustCompile(`^(.+?)\s+([a-z]{2,3}_[A-Za-z0-9]+)\s+#`)

// parseMacVoices reads `say -v ?`: "Name   lang_RE   # sample".
func parseMacVoices(out string) []sayVoice {
	var voices []sayVoice
	for _, line := range strings.Split(out, "\n") {
		if m := sayVoiceLine.FindStringSubmatch(line); m != nil {
			voices = append(voices, sayVoice{Name: strings.TrimSpace(m[1]), Language: strings.ReplaceAll(m[2], "_", "-"), Engine: "say"})
		}
	}
	return voices
}

// ---- speak while acting ----------------------------------------------------

// splitDashDash splits `say <text> [flags] -- <command...>`.
func splitDashDash(args []string) ([]string, []string, bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return nil, nil, false
}

// runSayWhile starts the speech, runs the command at the same time, and
// waits for both. The exit code is the command's; a speech failure only warns.
// --then-wait=false returns as soon as the command ends.
func runSayWhile(c *cli.Ctx, sayArgs, cmdArgs []string) error {
	thenWait := true
	var rest []string
	for _, a := range sayArgs {
		switch a {
		case "--then-wait=false", "--then-wait=no", "--then-wait=0":
			thenWait = false
		case "--then-wait", "--then-wait=true", "--then-wait=yes", "--then-wait=1":
		default:
			rest = append(rest, a)
		}
	}
	if _, err := parseSay(withoutFlag(rest, "async")); err != nil {
		return err
	}
	if len(cmdArgs) == 0 {
		return fmt.Errorf("nothing after `--`: say \"text\" -- aos <command>")
	}
	speech, err := startSpeech(withoutFlag(rest, "async"), !thenWait)
	if err != nil {
		fmt.Fprintf(c.Stderr, "aos say: could not start speaking: %v\n", err)
	}
	code := runAosOrExternal(c, cmdArgs)
	if speech != nil && thenWait {
		if err := speech.Wait(); err != nil {
			fmt.Fprintf(c.Stderr, "aos say: speech failed: %v\n", err)
		}
	} else if speech != nil {
		_ = speech.Process.Release()
	}
	if code != 0 {
		return &cli.ExitError{Code: code}
	}
	return nil
}

// startSpeech runs `aos say <args>` as a child process. A child (rather than a
// goroutine) keeps the speaker queue keyed by process, so parallel lines from
// one batch still take turns.
func startSpeech(args []string, detached bool) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(self, append([]string{"say"}, args...)...)
	cmd.Stderr = os.Stderr
	if detached {
		detach(cmd)
	} else {
		hideChild(cmd)
	}
	return cmd, cmd.Start()
}

// runAosOrExternal runs `aos ...` in this process (no startup, no quoting),
// anything else as a program, and returns its exit code.
func runAosOrExternal(c *cli.Ctx, argv []string) int {
	if isAosWord(argv[0]) {
		return cli.Run(c, argv[1:])
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, c.Stdout, c.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(c.Stderr, "aos: %v\n", err)
		return 127
	}
	return 0
}

func isAosWord(w string) bool {
	base := strings.ToLower(filepath.Base(w))
	return base == "aos" || base == "aos.exe"
}
