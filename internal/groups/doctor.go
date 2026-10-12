package groups

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/obs"
	"github.com/muthuishere/aos/internal/sys"
)

// checkStatus is a check's verdict. warn means degraded but usable; fail means
// something a user asked for will not work.
type checkStatus string

const (
	statusOK   checkStatus = "ok"
	statusWarn checkStatus = "warn"
	statusFail checkStatus = "fail"
)

// checkResult is one diagnosis. Remedy is the point of the whole command: a
// check that reports a problem without saying what to do about it has only
// moved the work.
type checkResult struct {
	Name   string      `json:"name"`
	Status checkStatus `json:"status"`
	Detail string      `json:"detail"`
	Remedy string      `json:"remedy,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("doctor", "Check that this machine can do what the commands promise")
		r.Add(&cli.Command{
			Group:   "doctor",
			Summary: "Diagnose this installation and say how to fix what is broken",
			Args:    "[--json]",
			Examples: []string{
				"aos doctor",
				"aos doctor --json",
			},
			Run: runDoctor,
		})
	})
}

func runDoctor(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}

	// Checks run in the order a user would care about them: what is broken
	// outright, then what is degraded, then what is merely informational.
	results := []checkResult{
		checkRegistry(c),
		checkTelemetry(c),
		checkDisplay(c),
		checkPermissions(c),
		checkWindowBackend(c),
		checkScreenshot(c),
		checkVideo(c),
		checkClipboard(c),
		checkPackageManager(c),
		checkPlugins(c),
	}

	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return err
		}
		return doctorExit(results)
	}

	width := 0
	for _, result := range results {
		if len(result.Name) > width {
			width = len(result.Name)
		}
	}
	for _, result := range results {
		c.Printf("%-4s %-*s  %s\n", result.Status, width, result.Name, result.Detail)
	}

	var remedies []checkResult
	for _, result := range results {
		if result.Status != statusOK && result.Remedy != "" {
			remedies = append(remedies, result)
		}
	}
	if len(remedies) > 0 {
		c.Println()
		c.Println("To fix:")
		for _, result := range remedies {
			c.Printf("  %s: %s\n", result.Name, result.Remedy)
		}
	}
	return doctorExit(results)
}

// doctorExit fails the process only on a hard failure. A warning is a report,
// not an error, so `doctor` stays usable in a health-check loop.
func doctorExit(results []checkResult) error {
	for _, result := range results {
		if result.Status == statusFail {
			return &cli.ExitError{Code: 1}
		}
	}
	return nil
}

func checkRegistry(c *cli.Ctx) checkResult {
	total := len(c.Registry.Commands())
	available := 0
	for _, cmd := range c.Registry.Commands() {
		if cmd.Supports(c.GOOS) {
			available++
		}
	}
	if problems := c.Registry.Warnings(); len(problems) > 0 {
		return checkResult{"registry", statusFail,
			fmt.Sprintf("%d registration problems", len(problems)),
			"run `aos commands --check` for the list"}
	}
	return checkResult{"registry", statusOK,
		fmt.Sprintf("%d of %d commands available on %s", available, total, c.GOOS), ""}
}

func checkTelemetry(c *cli.Ctx) checkResult {
	if obs.Disabled(c.Env) {
		return checkResult{"telemetry", statusWarn, "off via AOS_TELEMETRY",
			"unset AOS_TELEMETRY to record what this machine is asked to do"}
	}
	dir := obs.Dir(c.Env)
	// A functional check: telemetry that cannot be written is worse than none,
	// because it looks enabled.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return checkResult{"telemetry", statusFail, "cannot create " + dir,
			"set AOS_TELEMETRY_DIR to a writable path"}
	}
	probe := filepath.Join(dir, ".doctor-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return checkResult{"telemetry", statusFail, "cannot write to " + dir,
			"set AOS_TELEMETRY_DIR to a writable path"}
	}
	os.Remove(probe)
	return checkResult{"telemetry", statusOK, "recording to " + dir, ""}
}

func checkDisplay(c *cli.Ctx) checkResult {
	if cli.HasDisplay(c.Env, c.GOOS) {
		return checkResult{"display", statusOK, "available; GUI commands will run", ""}
	}
	remedy := "this is normal on a server; GUI commands are hidden"
	if c.GOOS == "linux" {
		remedy = "run `aos headless start` for a virtual display"
	}
	return checkResult{"display", statusWarn, "none; GUI commands are unavailable", remedy}
}

func checkPermissions(c *cli.Ctx) checkResult {
	if c.GOOS != "darwin" {
		return checkResult{"permissions", statusOK, "not gated on " + c.GOOS, ""}
	}
	accessibility, screen := engine.CheckAccessibility(), engine.CheckScreenCapture()
	switch {
	case accessibility && screen:
		return checkResult{"permissions", statusOK, "accessibility and screen capture granted", ""}
	case !accessibility && !screen:
		return checkResult{"permissions", statusFail, "accessibility and screen capture denied",
			"run `aos permission request`"}
	case !accessibility:
		return checkResult{"permissions", statusFail, "accessibility denied; window and input commands will fail",
			"run `aos permission request accessibility`"}
	default:
		return checkResult{"permissions", statusFail, "screen capture denied; screenshots will be blank",
			"run `aos permission request screen`"}
	}
}

// checkWindowBackend actually asks for the window list rather than testing
// whether a helper binary exists: on Linux the answer depends on a window
// manager being up, which no presence check can tell you.
func checkWindowBackend(c *cli.Ctx) checkResult {
	if !cli.HasDisplay(c.Env, c.GOOS) {
		return checkResult{"windows", statusWarn, "skipped; no display", ""}
	}
	if c.GOOS == "linux" {
		if missing := missingTools("wmctrl", "xdotool", "xrandr"); len(missing) > 0 {
			return checkResult{"windows", statusFail,
				"missing: " + strings.Join(missing, ", "),
				"install them: `aos pkg install " + strings.Join(missing, " ") + "`"}
		}
	}
	windows, err := engine.ListWindows(engine.Filter{})
	if err != nil {
		return checkResult{"windows", statusFail, "cannot list windows: " + err.Error(),
			"check that a window manager is running"}
	}
	return checkResult{"windows", statusOK, fmt.Sprintf("%d windows visible", len(windows)), ""}
}

// checkScreenshot captures for real and inspects the file, because a capture
// that silently produces an empty image is the failure mode that matters.
func checkScreenshot(c *cli.Ctx) checkResult {
	if !cli.HasDisplay(c.Env, c.GOOS) {
		return checkResult{"screenshot", statusWarn, "skipped; no display", ""}
	}
	path := filepath.Join(os.TempDir(), fmt.Sprintf("aos-doctor-%d.png", time.Now().UnixNano()))
	defer os.Remove(path)

	monitor := 1
	if _, err := engine.Screenshot(engine.ScreenshotOptions{Monitor: &monitor, OutPath: path}); err != nil {
		return checkResult{"screenshot", statusFail, err.Error(), screenshotRemedy(c.GOOS)}
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return checkResult{"screenshot", statusFail, "captured an empty image", screenshotRemedy(c.GOOS)}
	}
	return checkResult{"screenshot", statusOK, fmt.Sprintf("captured %d bytes", info.Size()), ""}
}

// checkVideo reports whether `capture video` can record here: ffmpeg on PATH,
// Screen Recording on macOS, and which audio sources exist.
func checkVideo(c *cli.Ctx) checkResult {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return checkResult{"video", statusWarn, "ffmpeg not found; `capture video` is unavailable", "install ffmpeg: " + ffmpegInstallHint(c.GOOS)}
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return checkResult{"video", statusWarn, "ffprobe not found; recordings cannot be rendered", "install ffmpeg (it ships ffprobe): " + ffmpegInstallHint(c.GOOS)}
	}
	if c.GOOS != "darwin" {
		return checkResult{"video", statusOK, "ffmpeg " + ffmpeg, ""}
	}
	if !engine.CheckScreenCapture() {
		return checkResult{"video", statusFail, "Screen Recording not granted; ffmpeg cannot see the screen",
			"grant Screen Recording: `aos permission request screen`"}
	}
	_, audio := parseAVFDevices(listAVFDevices(ffmpeg))
	loopback := "no loopback device (system audio needs one, e.g. BlackHole)"
	if d, ok := pickAudioDevice(audio, "", true); ok {
		loopback = "system audio via " + d.Name
	}
	detail := fmt.Sprintf("ffmpeg ok; %d audio input(s); %s; macOS asks for Microphone access on the first --audio=mic recording", len(audio), loopback)
	return checkResult{"video", statusOK, detail, ""}
}

func screenshotRemedy(goos string) string {
	if goos == "darwin" {
		return "grant Screen Recording: `aos permission request screen`"
	}
	return "install a capture tool: `aos pkg install scrot`"
}

// checkClipboard round-trips a value, which is the only way to know the
// clipboard tooling is wired up rather than merely installed.
func checkClipboard(c *cli.Ctx) checkResult {
	if !cli.HasDisplay(c.Env, c.GOOS) {
		return checkResult{"clipboard", statusWarn, "skipped; no display", ""}
	}
	probe := fmt.Sprintf("aos-doctor-%d", time.Now().UnixNano())
	if err := clipboardWrite(probe); err != nil {
		return checkResult{"clipboard", statusFail, "cannot write: " + err.Error(), clipboardRemedy(c.GOOS)}
	}
	got, err := clipboardRead()
	if err != nil {
		return checkResult{"clipboard", statusFail, "cannot read: " + err.Error(), clipboardRemedy(c.GOOS)}
	}
	if strings.TrimSpace(got) != probe {
		return checkResult{"clipboard", statusWarn, "round trip did not return what was written", clipboardRemedy(c.GOOS)}
	}
	return checkResult{"clipboard", statusOK, "round trip succeeded", ""}
}

func clipboardRemedy(goos string) string {
	if goos == "linux" {
		return "install a clipboard tool: `aos pkg install wl-clipboard` or xclip"
	}
	return ""
}

func checkPackageManager(c *cli.Ctx) checkResult {
	manager := detectPackageManager()
	if manager == nil {
		return checkResult{"packages", statusWarn, "no supported package manager found",
			"install one (homebrew, winget, apt, pacman) for the `pkg` commands"}
	}
	return checkResult{"packages", statusOK, manager.Name, ""}
}

func checkPlugins(c *cli.Ctx) checkResult {
	external := 0
	for _, cmd := range c.Registry.Commands() {
		if cmd.Binary != "" {
			external++
		}
	}
	dirs := cli.PluginDirs(c.Env)
	return checkResult{"plugins", statusOK,
		fmt.Sprintf("%d external commands across %d directories", external, len(dirs)), ""}
}

func missingTools(names ...string) []string {
	var missing []string
	for _, name := range names {
		if !sys.Has(name) {
			missing = append(missing, name)
		}
	}
	return missing
}
