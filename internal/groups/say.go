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
	"sync"
	"sync/atomic"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// `aos say` speaks text with the machine's own free voices: `say` on macOS,
// SAPI 5 over COM on Windows (the OneCore voices first, then the desktop
// ones), called from Go with no PowerShell. Text is split into caption-sized
// chunks; each is rendered to an audio file and played while the same text is
// shown as an aos subtitle, so voice and caption stay in step.
//
// Speakers queue: a second `say` (async or not) waits for the first to finish,
// so lines never talk over each other. State lives under
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
		r.Describe("say", "Speak text aloud with the machine's built-in voices, shown as a subtitle while it plays")
		plat := []string{"darwin", "windows"}
		r.Add(
			&cli.Command{
				Group: "say", Name: "",
				Summary: "Speak text aloud (or render it to an mp3/wav/aiff); --async returns at once",
				Args:    "<text...> [--voice=<name>] [--rate=<words per minute>] [--out=<file.mp3|.wav|.aiff>] [--no-subtitle] [--async] [--json] [-- <command...> [--then-wait=false]] [any `subtitle show` style flag: --position --size --color --bg --font --weight --align --max-width --margin --app/--title ...]",
				Examples: []string{
					`aos say "Now I open Notepad and type the note" -- aos key type --app=Notepad "hello"   # speak AND act, wait for both`,
					`aos say "build finished"`,
					`aos say "Hello there" --voice=Samantha --rate=190`,
					`aos say "welcome to the demo" --out=~/Desktop/welcome.mp3`,
					`aos say "now I open the settings" --async   # keeps talking while the script goes on`,
					"aos say wait",
					"# the text is shown as a subtitle while it is spoken, cleared when it ends; --no-subtitle turns that off",
					`aos say "Saving the report now" --position=top-center --size=L --app=Notepad   # caption style: the subtitle show flags`,
					"# long text is captioned a sentence (or clause, ~2 lines) at a time, each timed to its own audio",
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
	caption bool     // show the text as a subtitle while it is spoken
	style   []string // subtitle style flags, passed through to `subtitle show`
	json    bool
}

func parseSay(args []string) (sayOptions, error) {
	var o sayOptions
	styleFlags := sayCaptionFlags()
	set, err := parseArgs(args, append(append([]string{}, sayValueFlags...), styleFlags...)...)
	if err != nil {
		return o, err
	}
	known := append(append([]string{"async", "json", "no-subtitle"}, sayValueFlags...), styleFlags...)
	if err := set.Reject(known...); err != nil {
		return o, err
	}
	for _, f := range styleFlags {
		if set.Has(f) {
			o.style = append(o.style, "--"+f+"="+set.String(f, ""))
		}
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
	o.async, o.json = set.Has("async"), set.Has("json")
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
	// An unknown voice is an error here, before anything is queued: macOS
	// `say` would silently fall back to the default voice.
	if err := checkVoice(o.req.Voice); err != nil {
		return err
	}
	if o.async {
		return spawnSay(c, withoutFlag(args, "async"))
	}
	if o.out != "" { // a file only: nothing is played or shown
		return sayToFile(c, o)
	}
	dir := sayDir(c)
	if err := sayEnqueue(dir); err != nil {
		return err
	}
	defer sayDequeue(dir)

	// Long text becomes caption-sized chunks (a sentence, else a clause, about
	// two lines at most). Each chunk is its own clip, so its caption goes up
	// when its audio starts and is replaced when the next chunk starts.
	chunks := []string{o.req.Text}
	if o.caption {
		chunks = captionChunks(o.req.Text, captionMaxChars)
	}
	// Render every chunk at once: the engine runs per clip, so in parallel the
	// first words start about as soon as a single line would.
	clips := make([]string, len(chunks))
	errs := make([]error, len(chunks))
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func(i int, text string) {
			defer wg.Done()
			req := o.req
			req.Text = text
			clips[i], errs[i] = sayRenderClip(req)
		}(i, chunk)
	}
	wg.Wait()
	for _, clip := range clips {
		if clip != "" {
			defer os.Remove(clip)
		}
	}
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	dur := 0.0
	captioned := false
	for i, clip := range clips {
		d := audioDuration(clip)
		dur += d
		// The caption goes up while the audio starts, so the speech never
		// waits on the overlay (which takes a moment to appear). +1 s keeps it
		// up until the next chunk's caption replaces it, so there is no blank
		// flash; the last one is cleared when the speech ends.
		var shown chan bool
		if o.caption {
			shown = make(chan bool, 1)
			go func(text string, secs float64) { shown <- showSayCaption(c, text, secs+1, o.style) }(chunks[i], d)
		}
		if i == 0 {
			signalSpeechStarted()
		}
		err := playClip(dir, clip)
		if shown != nil && <-shown {
			captioned = true
		}
		if err != nil {
			return err
		}
	}
	if captioned {
		cli.Invoke(c, []string{"subtitle", "stop"}, "")
	}
	return sayReport(c, o, map[string]any{"Spoken": true, "Seconds": dur, "Chunks": len(chunks), "Subtitle": captioned})
}

// showSayCaption puts the spoken text up as a subtitle for the clip's length.
// A caption that cannot be shown (no display, say) never fails the speech.
func showSayCaption(c *cli.Ctx, text string, seconds float64, style []string) bool {
	secs := math.Max(1, math.Ceil(seconds*10)/10)
	argv := append([]string{"subtitle", "show", text, "--seconds=" + strconv.FormatFloat(secs, 'f', 1, 64)}, style...)
	res := cli.Invoke(c, argv, "")
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

// sayRenderClip renders one utterance to a temp file in the native format.
func sayRenderClip(req sayRequest) (string, error) {
	folder := os.TempDir()
	path := filepath.Join(folder, fmt.Sprintf("say-%d-%d-%d%s", time.Now().UnixMilli(), os.Getpid(), clipSeq.Add(1), sayNativeExt))
	if err := sayRender(req, path); err != nil {
		return "", err
	}
	return path, nil
}

// clipSeq keeps clip names unique when chunks render in parallel.
var clipSeq atomic.Int64

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
	speech, started, err := startSpeechSignalled(withoutFlag(rest, "async"), !thenWait)
	if err != nil {
		fmt.Fprintf(c.Stderr, "aos say: could not start speaking: %v\n", err)
		speech = nil
	}
	started() // act when the voice is heard, not while it is still rendering
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
	cmd, _, err := startSpeechSignalled(args, detached)
	return cmd, err
}

// sayStartedEnv names the file a `say` child creates the moment its audio
// starts playing. Rendering speech takes a while (about 2 s on Windows), so an
// action started together with the `say` would otherwise run ahead of the
// voice; callers wait for this signal first.
const sayStartedEnv = "AOS_SAY_STARTED"

// startSpeechSignalled starts `aos say <args>` and returns a function that
// blocks until its voice is heard (or it exits, or 20 s pass).
func startSpeechSignalled(args []string, detached bool) (*exec.Cmd, func(), error) {
	self, err := os.Executable()
	if err != nil {
		return nil, func() {}, err
	}
	marker := filepath.Join(os.TempDir(), fmt.Sprintf("aos-say-started-%d-%d", os.Getpid(), clipSeq.Add(1)))
	_ = os.Remove(marker)
	cmd := exec.Command(self, append([]string{"say"}, args...)...)
	cmd.Env = append(os.Environ(), sayStartedEnv+"="+marker)
	cmd.Stderr = os.Stderr
	if detached {
		detach(cmd)
	} else {
		hideChild(cmd)
	}
	if err := cmd.Start(); err != nil {
		return nil, func() {}, err
	}
	pid := cmd.Process.Pid
	started := func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			if fileExists(marker) {
				_ = os.Remove(marker)
				return
			}
			if !videoProcAlive(pid) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	return cmd, started, nil
}

// signalSpeechStarted tells a waiting caller the voice is now playing.
func signalSpeechStarted() {
	if marker := os.Getenv(sayStartedEnv); marker != "" {
		_ = os.WriteFile(marker, nil, 0o644)
	}
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

// checkVoice fails when a voice is named that is not installed. It matches
// like the engines do: the exact name, or part of it ("Zira").
func checkVoice(name string) error {
	if name == "" {
		return nil
	}
	voices, err := sayVoices()
	if err != nil {
		return nil // cannot list: let the engine decide
	}
	var names []string
	for _, v := range voices {
		names = append(names, v.Name)
		if strings.Contains(strings.ToLower(v.Name), strings.ToLower(name)) {
			return nil
		}
	}
	msg := fmt.Sprintf("no voice named %q", name)
	if s := closest(name, names); s != "" {
		msg += fmt.Sprintf(" (did you mean %s?)", s)
	}
	return &cli.ExitError{Code: 2, Message: msg + "; list them with `aos say voices`"}
}

// ---- captions -----------------------------------------------------------------

// sayCaptionFlags are the `subtitle show` style flags `say` passes through:
// the same list subtitle uses, minus the ones say owns (timing, preview).
func sayCaptionFlags() []string {
	var out []string
	for _, f := range subtitleValueFlags {
		switch f {
		case "seconds", "preview":
			continue
		}
		out = append(out, f)
	}
	return out
}

// captionMaxChars is about two lines of a default-size caption.
const captionMaxChars = 84

var (
	sentenceEnd = regexp.MustCompile(`([.!?…]+["')\]]*)\s+`)
	clauseEnd   = regexp.MustCompile(`([,;:—–])\s+`)
)

// captionChunks splits text for captions: sentences first; a sentence longer
// than max at clause marks; a clause still too long at word boundaries. Short
// neighbours are joined back up to one line, so captions do not flash.
func captionChunks(text string, max int) []string {
	text = strings.Join(strings.Fields(text), " ")
	if len([]rune(text)) <= max {
		return []string{text}
	}
	var pieces []string
	for _, sentence := range splitKeep(text, sentenceEnd) {
		if len([]rune(sentence)) <= max {
			pieces = append(pieces, sentence)
			continue
		}
		for _, clause := range splitKeep(sentence, clauseEnd) {
			if len([]rune(clause)) <= max {
				pieces = append(pieces, clause)
				continue
			}
			pieces = append(pieces, wrapWords(clause, max)...)
		}
	}
	// Join neighbours that still fit on about one line, and fold a very short
	// piece ("Done.") into the caption before it, so captions do not flash.
	var out []string
	for _, p := range pieces {
		n := len(out)
		if n == 0 {
			out = append(out, p)
			continue
		}
		joined := len([]rune(out[n-1])) + 1 + len([]rune(p))
		if joined <= max/2 || (len([]rune(p)) < 16 && joined <= max) {
			out[n-1] += " " + p
			continue
		}
		out = append(out, p)
	}
	return out
}

// splitKeep splits after each match, keeping the punctuation with its piece.
func splitKeep(s string, re *regexp.Regexp) []string {
	var out []string
	last := 0
	for _, m := range re.FindAllStringSubmatchIndex(s, -1) {
		out = append(out, strings.TrimSpace(s[last:m[3]]))
		last = m[1]
	}
	if rest := strings.TrimSpace(s[last:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

func wrapWords(s string, max int) []string {
	var out []string
	var cur []string
	n := 0
	for _, w := range strings.Fields(s) {
		if n > 0 && n+1+len([]rune(w)) > max {
			out = append(out, strings.Join(cur, " "))
			cur, n = nil, 0
		}
		if n > 0 {
			n++
		}
		cur = append(cur, w)
		n += len([]rune(w))
	}
	if len(cur) > 0 {
		out = append(out, strings.Join(cur, " "))
	}
	return out
}
