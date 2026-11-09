package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "watch", Name: "process",
			Summary:  "Wait for one process to end or appear, or print a JSON line for each start and stop",
			Blocking: true,
			Args:     "--name=<name> | --pid=<n> [--exit | --start] [--interval=<1s>] [--timeout=<dur>] [--json]",
			Examples: []string{
				"aos watch process --name=ffmpeg --exit && echo render done   # exit 0 when ffmpeg ends",
				"aos watch process --pid=4821 --exit --timeout=30m            # exit 3 if it is still running after 30m",
				"aos watch process --name=Xcode --start                       # exit 0 when Xcode appears",
				"aos watch process --name=node                                # a JSON line per start and stop",
			},
			Run: runWatchProcess,
		})
	})
}

// wprocEvent is one line of the stream. exit_code is only set when the OS
// gives it: Windows does for a process held open from the start; macOS and
// Linux never do for a process that is not our own child.
type wprocEvent struct {
	Event    string `json:"event"` // start | stop
	At       string `json:"at"`
	PID      int    `json:"pid"`
	Name     string `json:"name"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// wprocSnapshot maps pid to process name for the processes being watched.
type wprocSnapshot map[int]string

// wprocSelect keeps the processes that match: by pid, or by name compared
// whole and ignoring case. Whole, not substring as in `process find`: waiting
// on "sleep" must not be satisfied by "sleepd".
func wprocSelect(all []processInfo, name string, pid int) wprocSnapshot {
	snap := wprocSnapshot{}
	for _, p := range all {
		if (pid > 0 && p.PID == pid) || (pid == 0 && strings.EqualFold(p.Name, name)) {
			snap[p.PID] = p.Name
		}
	}
	return snap
}

// wprocDiff is what appeared in cur and what vanished from prev, in pid order.
func wprocDiff(prev, cur wprocSnapshot) (started, stopped []int) {
	for pid := range cur {
		if _, ok := prev[pid]; !ok {
			started = append(started, pid)
		}
	}
	for pid := range prev {
		if _, ok := cur[pid]; !ok {
			stopped = append(stopped, pid)
		}
	}
	sort.Ints(started)
	sort.Ints(stopped)
	return started, stopped
}

func sortedPIDs(s wprocSnapshot) []int {
	pids := make([]int, 0, len(s))
	for p := range s {
		pids = append(pids, p)
	}
	sort.Ints(pids)
	return pids
}

// wprocWatcher holds everything the loop touches outside itself so tests can
// drive it with a script of snapshots and a fake clock.
type wprocWatcher struct {
	list     func() ([]processInfo, error)
	hold     func(pid int)             // called for each pid seen, so the OS keeps its exit code
	exitCode func(pid int) (int, bool) // the exit code of a pid that stopped, if the OS gave it
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) bool // false when ctx ended
	emit     func(wprocEvent)
}

type wprocMode int

const (
	wprocStream wprocMode = iota
	wprocUntilExit
	wprocUntilStart
)

// wprocResult says why run returned.
type wprocResult int

const (
	wprocDone wprocResult = iota
	wprocTimedOut
	wprocInterrupted
	wprocNotRunning // --exit on something that is not running
)

func (w *wprocWatcher) run(ctx context.Context, name string, pid int, mode wprocMode, interval, timeout time.Duration) (wprocResult, error) {
	begin := w.now()
	all, err := w.list()
	if err != nil {
		return wprocDone, err
	}
	prev := wprocSelect(all, name, pid)
	for p := range prev {
		w.hold(p)
	}
	if mode == wprocUntilExit && len(prev) == 0 {
		return wprocNotRunning, nil
	}
	if mode == wprocStream {
		for _, p := range sortedPIDs(prev) {
			w.emit(wprocEvent{Event: "start", At: w.now().Format(time.RFC3339), PID: p, Name: prev[p]})
		}
	}
	// Only the processes present at the start count for --exit: one that
	// appears later must not keep the wait open.
	waiting := len(prev)
	for {
		wait := interval
		if timeout > 0 {
			left := timeout - w.now().Sub(begin)
			if left <= 0 {
				return wprocTimedOut, nil
			}
			if left < wait {
				wait = left
			}
		}
		if !w.sleep(ctx, wait) {
			return wprocInterrupted, nil
		}
		all, err := w.list()
		if err != nil {
			continue // one failed sample is not the end of a long wait
		}
		cur := wprocSelect(all, name, pid)
		started, stopped := wprocDiff(prev, cur)
		for _, p := range started {
			w.hold(p)
			if mode == wprocStream {
				w.emit(wprocEvent{Event: "start", At: w.now().Format(time.RFC3339), PID: p, Name: cur[p]})
			}
			if mode == wprocUntilStart {
				w.emit(wprocEvent{Event: "start", At: w.now().Format(time.RFC3339), PID: p, Name: cur[p]})
				return wprocDone, nil
			}
		}
		for _, p := range stopped {
			ev := wprocEvent{Event: "stop", At: w.now().Format(time.RFC3339), PID: p, Name: prev[p]}
			if code, ok := w.exitCode(p); ok {
				ev.ExitCode = &code
			}
			if mode != wprocUntilStart {
				w.emit(ev)
			}
			if mode == wprocUntilExit {
				waiting--
			}
		}
		if mode == wprocUntilExit && waiting <= 0 {
			return wprocDone, nil
		}
		prev = cur
	}
}

func runWatchProcess(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "name", "pid", "interval", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("name", "pid", "interval", "timeout", "exit", "start", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`watch process` takes flags only; did you mean --name=%s?", set.Rest[0])
	}
	name := set.String("name", "")
	pid, err := set.Int("pid", 0)
	if err != nil {
		return err
	}
	if (name == "") == (pid <= 0) {
		return fmt.Errorf("give exactly one of --name=<name> or --pid=<n>")
	}
	if set.Has("exit") && set.Has("start") {
		return fmt.Errorf("--exit and --start are separate conditions; pick one")
	}
	if set.Has("start") && name == "" {
		return fmt.Errorf("--start needs --name: a pid cannot appear before it exists")
	}
	mode := wprocStream
	switch {
	case set.Has("exit"):
		mode = wprocUntilExit
	case set.Has("start"):
		mode = wprocUntilStart
	}
	interval, err := parseInterval(set.String("interval", ""), time.Second)
	if err != nil {
		return err
	}
	if interval < minWatchInterval {
		return fmt.Errorf("--interval must be at least %s", minWatchInterval)
	}
	timeout, err := wprocTimeout(set.String("timeout", ""))
	if err != nil {
		return err
	}
	// The stream is always JSON lines; a condition prints a plain line unless --json.
	asJSON := set.Has("json") || mode == wprocStream

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	w := &wprocWatcher{
		list:     watchListProcesses,
		hold:     watchHoldProcess,
		exitCode: watchProcessExitCode,
		now:      time.Now,
		sleep: func(ctx context.Context, d time.Duration) bool {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return false
			case <-t.C:
				return true
			}
		},
		emit: func(e wprocEvent) {
			if asJSON {
				line, _ := json.Marshal(e)
				c.Printf("%s\n", line)
				return
			}
			c.Println(wprocText(e))
		},
	}
	target := name
	if target == "" {
		target = "pid " + strconv.Itoa(pid)
	}
	res, err := w.run(ctx, name, pid, mode, interval, timeout)
	if err != nil {
		return err
	}
	switch res {
	case wprocNotRunning:
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("%s is not running", target)}
	case wprocTimedOut:
		return &cli.ExitError{Code: 3, Message: fmt.Sprintf("timed out after %s waiting for %s", timeout, target)}
	case wprocInterrupted:
		if mode == wprocStream {
			return nil
		}
		return &cli.ExitError{Code: 130}
	}
	return nil
}

func wprocText(e wprocEvent) string {
	line := fmt.Sprintf("%s  %d  %s", e.Event, e.PID, e.Name)
	if e.ExitCode != nil {
		line += fmt.Sprintf("  exit code %d", *e.ExitCode)
	}
	return line
}

// wprocTimeout reads --timeout: a Go duration, or a bare number of seconds.
// Empty means wait forever.
func wprocTimeout(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	if secs, err := strconv.Atoi(value); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--timeout must be a positive duration like 90s or 30m, got %q", value)
	}
	return d, nil
}
