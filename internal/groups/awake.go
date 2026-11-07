package groups

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// A keep-awake is a small detached child that holds the machine's sleep
// assertion for as long as it lives: `caffeinate` on macOS, `systemd-inhibit`
// on Linux, and aos itself (`power awake-hold`) on Windows, where the
// assertion belongs to a thread. Ending the child ends the hold, so a crash
// or a reboot can never leave the machine stuck awake. Its pid is recorded in
// the cache dir so `off` and `status` can find it from a later call.

// awakeState is the record of the live keep-awake, and `status --json`.
type awakeState struct {
	Active  bool   `json:"active"`
	PID     int    `json:"pid,omitempty"`
	Display bool   `json:"display"`
	Started string `json:"started,omitempty"`
	// Until is when the hold ends by itself, "" for an open-ended one.
	Until string `json:"until,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "power", Name: "awake",
				Summary: "Keep the machine from sleeping, for a while or until `power awake off`",
				Args:    "[--for=<duration>] [--display] [--json]",
				Examples: []string{
					"aos power awake --for=2h",
					"aos power awake --for=30m --display",
					"aos power awake            # until `aos power awake off`",
				},
				Run: runPowerAwake,
			},
			&cli.Command{
				Group: "power", Name: "awake off",
				Summary:  "End the keep-awake started by `power awake`",
				Examples: []string{"aos power awake off"},
				Run:      runPowerAwakeOff,
			},
			&cli.Command{
				Group: "power", Name: "awake status",
				Summary: "Print whether a keep-awake is active and until when",
				Args:    "[--json]",
				Examples: []string{
					"aos power awake status",
					"aos power awake status --json",
				},
				Run: runPowerAwakeStatus,
			},
			&cli.Command{
				Group: "power", Name: "awake-hold",
				Summary:   "Hold the Windows keep-awake assertion (started by `power awake`)",
				Args:      "[--seconds=<n>] [--display]",
				Examples:  []string{"aos power awake-hold --seconds=7200"},
				Hidden:    true,
				Platforms: []string{"windows"},
				Run:       runPowerAwakeHold,
			},
		)
	})
}

func runPowerAwake(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "for")
	if err != nil {
		return err
	}
	if err := set.Reject("for", "display", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("unknown argument %q; did you mean `power awake off` or `power awake status`?", set.Rest[0])
	}
	dur, err := parseAwakeFor(set.String("for", ""))
	if err != nil {
		return err
	}
	display := set.Has("display")

	// One hold at a time: a new request replaces the old one, so the latest
	// duration is the one that counts.
	stopAwake()
	name, cmdArgs, err := awakeCommand(dur, display)
	if err != nil {
		return err
	}
	cmd := exec.Command(name, cmdArgs...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	now := time.Now()
	st := awakeState{Active: true, PID: cmd.Process.Pid, Display: display, Started: now.Format(time.RFC3339)}
	if dur > 0 {
		st.Until = now.Add(dur).Format(time.RFC3339)
	}
	if err := saveAwake(st); err != nil {
		return err
	}
	if set.Has("json") {
		return writeJSON(c, st)
	}
	c.Println(describeAwake(st))
	return nil
}

func runPowerAwakeOff(c *cli.Ctx, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`power awake off` takes no arguments")
	}
	if stopAwake() {
		c.Println("keep-awake off")
	} else {
		c.Println("no keep-awake was active")
	}
	return nil
}

func runPowerAwakeStatus(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("power awake status", args)
	if err != nil {
		return err
	}
	st := currentAwake()
	if asJSON {
		return writeJSON(c, st)
	}
	c.Println(describeAwake(st))
	return nil
}

func describeAwake(st awakeState) string {
	if !st.Active {
		return "keep-awake off"
	}
	what := "awake"
	if st.Display {
		what = "awake, display on"
	}
	if st.Until == "" {
		return fmt.Sprintf("%s until `aos power awake off` (pid %d)", what, st.PID)
	}
	return fmt.Sprintf("%s until %s (pid %d)", what, st.Until, st.PID)
}

// parseAwakeFor accepts Go durations ("2h", "90m", "1h30m"); "" means open-ended.
func parseAwakeFor(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Second {
		return 0, fmt.Errorf("--for wants a duration like 30m, 2h or 1h30m, got %q", s)
	}
	return d, nil
}

func awakePath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "aos", "awake.json")
}

func saveAwake(st awakeState) error {
	if err := os.MkdirAll(filepath.Dir(awakePath()), 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(st)
	return os.WriteFile(awakePath(), b, 0o644)
}

// currentAwake reads the record and drops it when its child is gone (the
// duration ran out, or something killed it).
func currentAwake() awakeState {
	b, err := os.ReadFile(awakePath())
	if err != nil {
		return awakeState{}
	}
	var st awakeState
	if json.Unmarshal(b, &st) != nil || !st.Active || !videoProcAlive(st.PID) {
		_ = os.Remove(awakePath())
		return awakeState{}
	}
	return st
}

// stopAwake ends the live keep-awake, reporting whether there was one.
func stopAwake() bool {
	st := currentAwake()
	_ = os.Remove(awakePath())
	if !st.Active {
		return false
	}
	if p, err := os.FindProcess(st.PID); err == nil {
		_ = p.Kill()
	}
	return true
}

// inhibitArgs builds the Linux systemd-inhibit line. "idle" is what blanks
// and locks the screen, so it is only taken with --display.
func inhibitArgs(d time.Duration, display bool) []string {
	what := "sleep"
	if display {
		what = "sleep:idle"
	}
	length := "infinity"
	if d > 0 {
		length = fmt.Sprint(int(d.Seconds()))
	}
	return []string{"--what=" + what, "--who=aos", "--why=aos power awake", "--mode=block", "sleep", length}
}

// caffeinateArgs builds the macOS caffeinate line: -i blocks idle sleep, -m
// disk sleep, -d also keeps the display on, -t ends the hold by itself.
func caffeinateArgs(d time.Duration, display bool) []string {
	args := []string{"-i", "-m"}
	if display {
		args = append(args, "-d")
	}
	if d > 0 {
		args = append(args, "-t", strconv.Itoa(int(d.Seconds())))
	}
	return args
}
