//go:build windows

// livetest is the focus-independent feature pass for aos on Windows, run by
// scripts\live-test.cmd. It asserts only on machine-checkable state (JSON
// results, WM_GETTEXT / UI Automation read-back, window list IDs,
// events.json), on one monitor (AOS_TEST_MONITOR, default 1). It prints
// PASS/FAIL per step and exits 1 if any step failed, 2 if the monitor is
// missing. Native Go only (see AGENTS.md).
//
//	livetest.exe [path\to\aos.exe]
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	aos         = "aos"
	mon         = 1
	mX, mY      int
	mW, mH      int
	fails, step int
)

// a runs aos and returns stdout+stderr and the exit code.
func a(args ...string) (string, int) {
	out, err := exec.Command(aos, args...).CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		return string(out) + err.Error(), -1
	}
	return string(out), 0
}

func aOut(args ...string) string { s, _ := a(args...); return s }

func pass(n string) { step++; fmt.Printf("PASS  %2d  %s\n", step, n) }

func fail(n, why string) {
	step++
	fails++
	fmt.Printf("FAIL  %2d  %s\n", step, n)
	if why = strings.TrimSpace(why); why != "" {
		fmt.Println("          " + regexp.MustCompile(`\r?\n`).ReplaceAllString(why, " "))
	}
}

// j parses the whole output as JSON, else its last line.
func j(raw string) any {
	var v any
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	lines := regexp.MustCompile(`\r?\n`).Split(strings.TrimSpace(raw), -1)
	if json.Unmarshal([]byte(lines[len(lines)-1]), &v) == nil {
		return v
	}
	return nil
}

// get walks maps by key; missing keys give nil.
func get(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

func num(v any) float64 { f, _ := v.(float64); return f }
func str(v any) string  { s, _ := v.(string); return s }
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	}
	return true
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

func check(name, raw string, ok func(any) bool) {
	v := j(raw)
	r := false
	if v != nil {
		func() { defer func() { recover() }(); r = ok(v) }()
	}
	if r {
		pass(name)
	} else {
		fail(name, clip(raw))
	}
}

// inside reports whether a {X,Y,Width,Height} rect lies on the test monitor.
func inside(r any) bool {
	x, y, w, h := num(get(r, "X")), num(get(r, "Y")), num(get(r, "Width")), num(get(r, "Height"))
	if get(r, "X") == nil {
		return false
	}
	return int(x) >= mX && int(y) >= mY && int(x+w) <= mX+mW && int(y+h) <= mY+mH
}

func windows() []any {
	l, _ := j(aOut("window", "list", "--json")).([]any)
	return l
}

func focusedID() string {
	for _, w := range windows() {
		if get(w, "Focused") == true {
			return str(get(w, "ID"))
		}
	}
	return ""
}

func findWindow(match func(title string) bool) any {
	for _, w := range windows() {
		if match(str(get(w, "Title"))) {
			return w
		}
	}
	return nil
}

func compact(v any) string { b, _ := json.Marshal(v); return string(b) }

func kill(pid int) {
	if pid > 0 {
		_ = exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
	}
}

func running(image string) bool {
	out, _ := exec.Command("tasklist", "/FI", "IMAGENAME eq "+image, "/NH").Output()
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(image))
}

func main() {
	if len(os.Args) > 1 && os.Args[1] != "" {
		aos = os.Args[1]
	}
	if v := os.Getenv("AOS_TEST_MONITOR"); v != "" {
		mon, _ = strconv.Atoi(v)
	}
	os.Setenv("AOS_NO_SKILL_AUTOINSTALL", "1")
	os.Exit(run())
}

func run() int {
	cwd, _ := os.Getwd()
	work := filepath.Join(cwd, fmt.Sprintf("aos-live-%08x", rand.Uint32()))
	if err := os.MkdirAll(work, 0o755); err != nil {
		fmt.Println(err)
		return 2
	}
	doc := filepath.Join(work, "aos-live-test.txt")
	_ = os.WriteFile(doc, nil, 0o644)
	out := filepath.Join(work, "live.mp4")

	// ---- monitor ----
	found := false
	re := regexp.MustCompile(`^\s*(\d+)\s+(\d+)x(\d+)([+-]\d+)([+-]\d+)`)
	for _, line := range regexp.MustCompile(`\r?\n`).Split(aOut("display", "list"), -1) {
		if m := re.FindStringSubmatch(line); m != nil {
			if n, _ := strconv.Atoi(m[1]); n == mon {
				mW, _ = strconv.Atoi(m[2])
				mH, _ = strconv.Atoi(m[3])
				mX, _ = strconv.Atoi(m[4])
				mY, _ = strconv.Atoi(m[5])
				found = true
			}
		}
	}
	if !found {
		fmt.Printf("monitor %d not found\n", mon)
		return 2
	}
	fmt.Printf("aos live test on monitor %d (%d,%d,%d,%d), work dir %s\n", mon, mX, mY, mW, mH, work)

	calcWas := running("CalculatorApp.exe")
	var notepad *exec.Cmd
	var np any
	defer func() {
		a("capture", "video", "stop", "--raw")
		a("draw", "stop")
		if notepad != nil && notepad.Process != nil {
			kill(notepad.Process.Pid)
		}
		if np != nil {
			kill(int(num(get(np, "PID"))))
		}
		if !calcWas {
			_ = exec.Command("taskkill", "/F", "/IM", "CalculatorApp.exe").Run()
		}
	}()

	// ---- windows this test needs, on the test monitor ----
	notepad = exec.Command("notepad.exe", doc)
	_ = notepad.Start()
	a("window", "wait", "--title=aos-live-test", "--timeout=15000")
	_ = exec.Command("cmd", "/c", "start", "", "calculator:").Run()
	a("window", "wait", "--title=Calculator", "--timeout=15000")
	ms := strconv.Itoa(mon)
	a("window", "move", "--title=aos-live-test", "--monitor="+ms, "--at=40,80,900,600")
	a("window", "move", "--title=Calculator", "--monitor="+ms, "--at=1000,80,420,640")
	time.Sleep(800 * time.Millisecond)
	np = findWindow(func(t string) bool { return strings.Contains(t, "aos-live-test") })
	if np != nil && int(num(get(np, "Monitor"))) == mon && inside(get(np, "Bounds")) {
		pass(fmt.Sprintf("Notepad test document is on monitor %d", mon))
	} else {
		fail(fmt.Sprintf("Notepad on monitor %d", mon), compact(np))
	}
	calc := findWindow(func(t string) bool { return t == "Calculator" })
	if calc != nil && int(num(get(calc, "Monitor"))) == mon {
		pass(fmt.Sprintf("Calculator is on monitor %d", mon))
	} else {
		fail(fmt.Sprintf("Calculator on monitor %d", mon), compact(calc))
	}
	npID := str(get(np, "ID"))

	// ---- recording ----
	rec := aOut("capture", "video", "start", "smart", "--monitor="+ms, "--out="+out, "--max=3m", "--json")
	check(fmt.Sprintf("smart recording started on monitor %d", mon), rec, func(v any) bool {
		return str(get(v, "Mode")) == "smart" && int(num(get(v, "Capture", "X"))) == mX && int(num(get(v, "Capture", "Width"))) == mW
	})

	// ---- 1. focus-free typing, verified by state ----
	before := focusedID()
	r := aOut("key", "type", "aos live test", "--title=aos-live-test", "--verify", "--json")
	check("key type --verify into Notepad (posted, WM_GETTEXT read back)", r, func(v any) bool {
		return str(get(v, "Route")) == "post" && truthy(get(v, "Verified")) &&
			strings.Contains(str(get(v, "Value")), "aos live test") && str(get(v, "Window", "ID")) == npID
	})
	r = aOut("key", "press", "enter", "--title=aos-live-test", "--json")
	check("key press enter into Notepad (posted)", r, func(v any) bool { return str(get(v, "Route")) == "post" })
	r = aOut("key", "type", "second line", "--title=aos-live-test", "--verify", "--json")
	check("second line verified after enter", r, func(v any) bool {
		val := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(str(get(v, "Value")))
		return truthy(get(v, "Verified")) && strings.Contains(val, "aos live test\nsecond line")
	})
	if after := focusedID(); after == before {
		pass(fmt.Sprintf("OS focus unchanged by focus-free input (focused: %s)", after))
	} else {
		fail("OS focus unchanged", fmt.Sprintf("before=%s after=%s", before, after))
	}
	if _, rc := a("key", "type", "x", "--app=NoSuchAppAOS", "--verify"); rc == 3 {
		pass("unknown target exits 3, types nothing")
	} else {
		fail("unknown target exits 3", fmt.Sprintf("rc=%d", rc))
	}
	if r, rc := a("key", "press", "ctrl+a", "--title=aos-live-test", "--json", "--verify"); rc != 0 || strings.Contains(r, `"Route":"focus"`) {
		pass("modifier chord routes to focus (or is refused) on Windows")
	} else {
		fail("modifier chord routes to focus", r)
	}

	// ---- 2. Calculator: UI Automation Invoke, no pointer ----
	cur0 := strings.TrimSpace(aOut("mouse", "position"))
	r = aOut("mouse", "click", "--title=Calculator", "--find=Seven", "--role=button", "--json")
	check("mouse click --find=Seven used UIA Invoke", r, func(v any) bool {
		return str(get(v, "Route")) == "press" && inside(get(v, "Found", "Bounds"))
	})
	if cur1 := strings.TrimSpace(aOut("mouse", "position")); cur0 == cur1 {
		pass(fmt.Sprintf("pointer did not move (%s)", cur1))
	} else {
		fail("pointer did not move", cur0+" -> "+cur1)
	}
	time.Sleep(500 * time.Millisecond)
	r = aOut("find", "--title=Calculator", "--text=Display is 7", "--source=accessibility", "--no-raise", "--json")
	check("Calculator display reads 7 (UI Automation)", r, func(v any) bool { return strings.Contains(str(get(v, "Label")), "7") })

	// ---- 3. overlays land on the recording's monitor ----
	onMon := func(v any) bool { return int(num(get(v, "Monitor"))) == mon && inside(get(v, "Sign")) }
	check("subtitle (no --monitor) on the recording's monitor", aOut("subtitle", "show", "aos live test: subtitle", "--seconds=2", "--json"), onMon)
	check("draw text (no --monitor) on the recording's monitor", aOut("draw", "text", "aos live test: label", "--duration=1", "--json"), onMon)
	check("draw --at is relative to the recording's monitor", aOut("draw", "--at=300,300", "--text=relative point", "--duration=1", "--json"), func(v any) bool {
		return int(num(get(v, "Monitor"))) == mon && int(num(get(v, "Tip", "X"))) == mX+300 && int(num(get(v, "Tip", "Y"))) == mY+300
	})
	check(fmt.Sprintf("draw --find on Calculator stays on monitor %d", mon),
		aOut("draw", "--title=Calculator", "--find=Seven", "--role=button", "--style=ring", "--text=press 7", "--duration=1", "--json"), onMon)

	// ---- 4. a move of the target, then window-list IDs confirm a close ----
	a("window", "move", "--title=aos-live-test", "--monitor="+ms, "--at=60,100,880,580")
	time.Sleep(1200 * time.Millisecond)
	stop := aOut("capture", "video", "stop", "--json")
	check("recording stopped and final cut rendered", stop, func(v any) bool {
		return truthy(get(v, "Final")) && num(get(v, "Duration")) > 2
	})
	sj := j(stop)
	// Win11 Notepad hands the file to its own (possibly shared) process, so
	// close the process that owns the listed window, not the launcher.
	kill(int(num(get(np, "PID"))))
	if notepad.Process != nil {
		kill(notepad.Process.Pid)
	}
	notepad = nil
	time.Sleep(800 * time.Millisecond)
	gone := npID != ""
	for _, w := range windows() {
		if str(get(w, "ID")) == npID {
			gone = false
		}
	}
	if gone {
		pass(fmt.Sprintf("closed Notepad's ID %s is gone from window list --json", npID))
	} else {
		fail("closed window's ID gone", npID)
	}

	// ---- 5. the recording's events ----
	eventsPath := str(get(sj, "Events"))
	data, err := os.ReadFile(eventsPath)
	if sj == nil || eventsPath == "" || err != nil {
		fail("events.json exists", stop)
	} else {
		var ev any
		_ = json.Unmarshal(data, &ev)
		capRect := get(ev, "capture")
		if int(num(get(capRect, "X"))) == mX && int(num(get(capRect, "Width"))) == mW {
			pass(fmt.Sprintf("events: capture area is monitor %d", mon))
		} else {
			fail("events: capture area", compact(capRect))
		}
		events, _ := get(ev, "events").([]any)
		var bad []any
		labels := map[string]bool{}
		var labelList, cmds []string
		moved := false
		for _, e := range events {
			kind := str(get(e, "kind"))
			if rect := get(e, "rect"); rect != nil && kind != "cursor" && !inside(rect) {
				bad = append(bad, e)
			}
			if kind == "window" {
				l := str(get(e, "label"))
				if !labels[l] {
					labels[l] = true
					labelList = append(labelList, l)
				}
				if l == "Notepad" && int(num(get(e, "rect", "X"))) == mX+60 {
					moved = true
				}
			}
			cmds = append(cmds, str(get(e, "cmd")))
		}
		if len(bad) == 0 {
			pass(fmt.Sprintf("events: every rect is inside monitor %d", mon))
		} else {
			fail(fmt.Sprintf("events: rects outside monitor %d", mon), compact(bad))
		}
		foreign := false
		for _, l := range labelList {
			if l != "Notepad" && l != "ApplicationFrameHost" {
				foreign = true
			}
		}
		if len(labelList) > 0 && !foreign {
			pass(fmt.Sprintf("events: camera windows are only aos targets (%s)", strings.Join(labelList, ", ")))
		} else {
			fail("events: camera windows only aos targets", strings.Join(labelList, ", "))
		}
		has := func(c string) bool {
			for _, x := range cmds {
				if x == c {
					return true
				}
			}
			return false
		}
		if has("key type") && has("mouse click") && has("draw") {
			pass("events: key/click/draw actions were logged")
		} else {
			fail("events: actions logged", strings.Join(cmds, ","))
		}
		if moved {
			pass("events: the target's move was logged")
		} else {
			fail("events: target move logged", "")
		}
		fmt.Println("final cut: " + str(get(sj, "Final")))
	}

	fmt.Println()
	if fails == 0 {
		fmt.Printf("ALL %d STEPS PASSED\n", step)
		return 0
	}
	fmt.Printf("%d OF %d STEPS FAILED\n", fails, step)
	return 1
}
