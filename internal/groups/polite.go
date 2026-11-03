package groups

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
	"github.com/muthuishere/aos/internal/obs"
)

// Polite mode. A person may be working at this machine while an agent drives
// aos. Anything that takes the foreground or moves the real pointer (window
// focus, keystrokes to whatever has focus, a coordinate click, --focus,
// --raise) first asks the OS how long the person has been idle; if they
// touched the keyboard or mouse in the last politeIdle, the command refuses
// with exit 5 instead of yanking focus out from under them.
//
// Overrides: --force on the command, AOS_FOCUS=allow, or AOS_POLITE=0.
// Input aos itself sent counts as idle time, so an agent's own sequence of
// commands is not mistaken for a person.

// exitUserActive: refused because the person at the machine is active.
const exitUserActive = 5

// politeIdle is how long the person must have been idle.
const politeIdle = 5 * time.Second

// idleFn reports how long since the last real keyboard / mouse input;
// ok=false when this platform cannot tell (the guard then lets it through).
var idleFn = userIdle

// politeNow is the clock, swapped in tests.
var politeNow = time.Now

// politeCheck refuses (exit 5) when the person was active recently. what
// names the action for the message.
func politeCheck(c *cli.Ctx, set *argSet, what string) error {
	idle, known := idleFn()
	refused := false
	defer func() {
		attrs := []obs.Attribute{
			obs.StringAttr("agentic_os.focus.action", what),
			obs.StringAttr("agentic_os.focus.caller", callerName(c)),
			obs.BoolAttr("agentic_os.focus.refused", refused),
		}
		if known {
			attrs = append(attrs, obs.IntAttr("agentic_os.focus.user_idle_ms", idle.Milliseconds()))
		}
		c.Annotate(attrs...)
	}()
	if set != nil && set.Has("force") {
		return nil
	}
	if c.Env("AOS_POLITE") == "0" || strings.EqualFold(c.Env("AOS_FOCUS"), "allow") {
		return nil
	}
	ok := known
	if !ok || idle >= politeIdle {
		return nil
	}
	// The last input may have been aos's own (the previous command of the
	// same agent). Synthetic events reset the OS idle clock too.
	if last, ok := lastOwnInput(); ok {
		lastInput := politeNow().Add(-idle)
		if !lastInput.After(last.Add(750 * time.Millisecond)) {
			return nil
		}
	}
	refused = true
	return &cli.ExitError{Code: exitUserActive, Message: fmt.Sprintf(
		"refused: %s would take focus or move the pointer, and someone used this machine %.1fs ago. "+
			"Target the window instead (--app/--title: key type --app=X, mouse click --app=X --find=T), "+
			"or pass --force (AOS_FOCUS=allow, or AOS_POLITE=0 to turn this off)",
		what, idle.Seconds())}
}

func ownInputPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "aos", "last-input")
}

// noteOwnInput records that aos just sent real input, so the next polite
// check does not count it as the person.
func noteOwnInput() {
	p := ownInputPath()
	if p == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte(strconv.FormatInt(politeNow().UnixNano(), 10)), 0o600)
}

func lastOwnInput() (time.Time, bool) {
	p := ownInputPath()
	if p == "" {
		return time.Time{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, n), true
}

// handBack remembers the focused window and the pointer and returns the
// function that restores both: a command that had to take them gives them
// back to the person afterwards.
func handBack() func() {
	var prev string
	if ws, err := engine.ListWindows(engine.Filter{}); err == nil {
		for _, w := range ws {
			if w.Focused {
				prev = w.ID
				break
			}
		}
	}
	px, py, perr := engine.CursorPosition()
	return func() {
		if perr == nil {
			_ = engine.MouseMove(nil, px, py)
		}
		if prev != "" {
			_ = engine.FocusWindowID(prev)
		}
	}
}

// callerName says who ran this command: the agent (from the environment it
// sets) and the parent process, so `aos obs focus` can show who took focus.
func callerName(c *cli.Ctx) string {
	agent := ""
	switch {
	case c.Source == "mcp":
		agent = "mcp client"
	case c.Env("CLAUDECODE") != "" || c.Env("CLAUDE_CODE_ENTRYPOINT") != "":
		agent = "claude-code"
	case c.Env("CODEX_SANDBOX") != "" || c.Env("CODEX_HOME") != "" || c.Env("CODEX_MANAGED_BY_NPM") != "":
		agent = "codex"
	case c.Env("CURSOR_TRACE_ID") != "":
		agent = "cursor"
	case c.Env("AGENTBUS_JOB") != "" || c.Env("AGENTBUS_NODE") != "":
		agent = "agentbus"
	}
	if who := c.Env("AOS_CALLER"); who != "" {
		agent = who
	}
	parent := parentName()
	switch {
	case agent != "" && parent != "":
		return agent + " via " + parent
	case agent != "":
		return agent
	case parent != "":
		return parent
	}
	return "unknown"
}

func parentName() string {
	ppid := os.Getppid()
	if runtime.GOOS == "windows" || ppid <= 1 {
		return ""
	}
	out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(ppid)).Output()
	if err != nil {
		return ""
	}
	return filepath.Base(strings.TrimSpace(string(out))) + " (pid " + strconv.Itoa(ppid) + ")"
}
