//go:build windows

// Package harness is the shared plumbing of the live Windows checks under
// test/windows: run aos.exe, read the foreground window, take before/after
// screenshots, record PASS/FAIL per step. Native Win32 only; aos never uses
// PowerShell (see AGENTS.md).
package harness

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW      = user32.NewProc("GetWindowTextW")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procSendMessageW        = user32.NewProc("SendMessageW")
	procFindWindowExW       = user32.NewProc("FindWindowExW")
	procEnumChildWindows    = user32.NewProc("EnumChildWindows")
	procGetClassNameW       = user32.NewProc("GetClassNameW")
	procGetWindowRect       = user32.NewProc("GetWindowRect")
)

// Message ids the checks post.
const (
	WMClose       = 0x0010
	WMMouseMove   = 0x0200
	WMLButtonDown = 0x0201
	WMLButtonUp   = 0x0202
	EMSetModify   = 0x00B9
)

// Dir is where the test exe lives; shots and results land next to it.
var Dir = func() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}()

// AOS is aos.exe next to the test, else aos on PATH.
var AOS = func() string {
	p := filepath.Join(Dir, "aos.exe")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "aos"
}()

// Setup sets the environment every check runs aos with.
func Setup(caller string) {
	os.Setenv("AOS_NO_SKILL_AUTOINSTALL", "1")
	os.Setenv("AOS_CALLER", caller)
}

// Aos runs aos.exe and returns stdout+stderr and the exit code.
func Aos(args ...string) (string, int) {
	cmd := exec.Command(AOS, args...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		code = -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return string(out), code
}

// Start launches a program without waiting.
func Start(name string, args ...string) *os.Process {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		fmt.Printf("  start %s: %v\n", name, err)
		return nil
	}
	return cmd.Process
}

// Tag is a short random marker for window titles and temp files.
func Tag(prefix string) string { return fmt.Sprintf("%s%d", prefix, rand.Intn(99999)) }

// Title is a window's caption.
func Title(h uintptr) string {
	buf := make([]uint16, 512)
	procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 512)
	return syscall.UTF16ToString(buf)
}

// Fg describes the foreground window as "<handle> '<title>'".
func Fg() string {
	h, _, _ := procGetForegroundWindow.Call()
	return fmt.Sprintf("%d '%s'", h, Title(h))
}

// Front makes a window the foreground and lets it settle.
func Front(h uintptr) {
	if h != 0 {
		procSetForegroundWindow.Call(h)
		time.Sleep(600 * time.Millisecond)
	}
}

func Post(h uintptr, msg uint32, w, l uintptr) { procPostMessageW.Call(h, uintptr(msg), w, l) }
func Send(h uintptr, msg uint32, w, l uintptr) { procSendMessageW.Call(h, uintptr(msg), w, l) }

// FindChild is FindWindowEx(parent, 0, class, nil).
func FindChild(parent uintptr, class string) uintptr {
	c, _ := syscall.UTF16PtrFromString(class)
	h, _, _ := procFindWindowExW.Call(parent, 0, uintptr(unsafe.Pointer(c)), 0)
	return h
}

// Child is one child window: handle, class and area.
type Child struct {
	H     uintptr
	Class string
	Area  int
}

// Children lists a window's descendants.
func Children(parent uintptr) []Child {
	var out []Child
	cb := syscall.NewCallback(func(h, _ uintptr) uintptr {
		buf := make([]uint16, 256)
		procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
		var r struct{ L, T, R, B int32 }
		procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
		out = append(out, Child{H: h, Class: syscall.UTF16ToString(buf), Area: int(r.R-r.L) * int(r.B-r.T)})
		return 1
	})
	procEnumChildWindows.Call(parent, cb, 0)
	return out
}

// Window is one row of `aos window list --json`.
type Window struct {
	ID    string `json:"ID"`
	Title string `json:"Title"`
	App   string `json:"App"`
}

// H is the window's handle.
func (w Window) H() uintptr {
	var h uint64
	fmt.Sscan(w.ID, &h)
	return uintptr(h)
}

// Windows is aos's window list.
func Windows() []Window {
	out, _ := Aos("window", "list", "--json")
	var ws []Window
	if i := strings.IndexByte(out, '['); i >= 0 {
		_ = json.Unmarshal([]byte(out[i:]), &ws)
	}
	return ws
}

// Find returns the windows whose title matches the glob-ish pattern (* only).
func Find(pattern string, app string) []Window {
	re := regexp.MustCompile("(?i)^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$")
	var out []Window
	for _, w := range Windows() {
		if (app == "" || strings.EqualFold(w.App, app)) && re.MatchString(w.Title) {
			out = append(out, w)
		}
	}
	return out
}

// ClearNotepadModified marks a Notepad window's edit control unmodified, so
// closing it never prompts to save.
func ClearNotepadModified(h uintptr) {
	ed := FindChild(h, "RichEditD2DPT")
	if ed == 0 {
		if tb := FindChild(h, "NotepadTextBox"); tb != 0 {
			ed = FindChild(tb, "RichEditD2DPT")
		}
	}
	if ed != 0 {
		Send(ed, EMSetModify, 0, 0)
	}
}

// CloseWindows posts WM_CLOSE to every window whose title matches.
func CloseWindows(pattern string) {
	for _, w := range Find(pattern, "") {
		ClearNotepadModified(w.H())
		Post(w.H(), WMClose, 0, 0)
	}
}

// KillTree ends a process and its children.
func KillTree(pid int) { _ = exec.Command("taskkill", "/T", "/F", "/PID", fmt.Sprint(pid)).Run() }

// Running reports whether a process image (e.g. CalculatorApp.exe) runs.
func Running(image string) bool {
	out, _ := exec.Command("tasklist", "/FI", "IMAGENAME eq "+image, "/NH").Output()
	return strings.Contains(strings.ToLower(string(out)), strings.ToLower(image))
}

// Shot saves a full-screen capture as shot-<name>.png next to the test.
func Shot(name string) {
	Aos("capture", "screenshot", "fullscreen", "--out="+filepath.Join(Dir, "shot-"+name+".png"))
}

// Cursor is `aos mouse position`.
func Cursor() string {
	out, _ := Aos("mouse", "position")
	return strings.TrimSpace(out)
}

// Result is one step's outcome.
type Result struct {
	Step, Result           string
	Landed, ForegroundKept bool
	PointerKept            *bool `json:",omitempty"`
	Exit                   int
	Shots                  string
}

// Suite runs steps and keeps their results.
type Suite struct {
	Name         string
	Settle       time.Duration // wait before reading the foreground again
	Before       time.Duration // wait before each step
	CheckPointer bool
	Results      []Result
	n            int
}

var nonWord = regexp.MustCompile(`[^A-Za-z0-9]+`)

// Check runs act, then landed(out); PASS needs it to land and the foreground
// (and, if asked, the pointer) to stay put.
func (s *Suite) Check(step string, act func() (string, int), landed func(out string) bool) {
	s.n++
	id := fmt.Sprintf("%02d-%s", s.n, strings.ToLower(strings.Trim(nonWord.ReplaceAllString(step, "-"), "-")))
	if s.Name != "" {
		id = fmt.Sprintf("%02d-%s-%s", s.n, s.Name, strings.ToLower(strings.Trim(nonWord.ReplaceAllString(step, "-"), "-")))
	}
	time.Sleep(s.Before)
	Shot(id + "-before")
	fg0, c0 := Fg(), ""
	if s.CheckPointer {
		c0 = Cursor()
	}
	out, code := act()
	time.Sleep(s.Settle)
	fg1 := Fg()
	r := Result{Step: step, Exit: code, Shots: id, Landed: landed(out), ForegroundKept: fg0 == fg1}
	ok := r.Landed && r.ForegroundKept
	if s.CheckPointer {
		kept := c0 == Cursor()
		r.PointerKept = &kept
		ok = ok && kept
	}
	Shot(id + "-after")
	r.Result = map[bool]string{true: "PASS", false: "FAIL"}[ok]
	s.Results = append(s.Results, r)
	fmt.Printf("[%s] %s  exit=%d landed=%v fgKept=%v", r.Result, step, code, r.Landed, r.ForegroundKept)
	if r.PointerKept != nil {
		fmt.Printf(" ptrKept=%v", *r.PointerKept)
	}
	fmt.Println()
	o := strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(out), "\r\n", " | "), "\n", " | ")
	if len(o) > 400 {
		o = o[:400]
	}
	fmt.Println("    out: " + o)
	if fg0 != fg1 {
		fmt.Printf("    foreground: %s -> %s\n", fg0, fg1)
	}
}

// Finish prints the table, writes shot-<file>.json and returns the exit code.
func (s *Suite) Finish(file string) int {
	fail := 0
	fmt.Println()
	for _, r := range s.Results {
		fmt.Printf("%-5s %s\n", r.Result, r.Step)
		if r.Result == "FAIL" {
			fail++
		}
	}
	data, _ := json.MarshalIndent(s.Results, "", "  ")
	_ = os.WriteFile(filepath.Join(Dir, file), data, 0o644)
	if fail > 0 {
		return 1
	}
	return 0
}

// AosStep wraps an aos call as a step action.
func AosStep(args ...string) func() (string, int) {
	return func() (string, int) { return Aos(args...) }
}

// Has is a regexp test on output.
func Has(pattern string) func(string) bool {
	re := regexp.MustCompile(pattern)
	return func(out string) bool { return re.MatchString(out) }
}
