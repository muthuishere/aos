//go:build windows

// Command hiddendesktop is a spike: can agent-launched apps live on a hidden
// secondary desktop, where no input aos sends can ever reach the owner's
// desktop? It creates desktop "aos-agent", starts testform.exe there, runs
// aos.exe ON that desktop to list / type / click / read it, checks the
// owner's foreground never changes, then closes everything and the desktop.
// Run hidden-desktop-spike.cmd with aos.exe and testform.exe next to it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	h "github.com/muthuishere/aos/test/windows/harness"
)

var (
	user32         = syscall.NewLazyDLL("user32.dll")
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	createDesktopW = user32.NewProc("CreateDesktopW")
	closeDesktop   = user32.NewProc("CloseDesktop")
	createProcessW = kernel32.NewProc("CreateProcessW")
	waitSingle     = kernel32.NewProc("WaitForSingleObject")
	getExitCode    = kernel32.NewProc("GetExitCodeProcess")
	closeHandle    = kernel32.NewProc("CloseHandle")
)

type startupInfo struct {
	Cb                                 uint32
	Reserved, Desktop, Title           *uint16
	X, Y, XSize, YSize, XChars, YChars uint32
	FillAttribute, Flags               uint32
	ShowWindow, Reserved2              uint16
	Reserved3, StdIn, StdOut, StdErr   uintptr
}

type processInfo struct {
	Process, Thread     uintptr
	ProcessID, ThreadID uint32
}

func u16(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

// run starts cmd on desk; with wait>0 it waits and returns the exit code.
func run(desk, cmd string, wait time.Duration) (pid int, exit uint32, err error) {
	si := startupInfo{Desktop: u16(desk)}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi processInfo
	line, _ := syscall.UTF16FromString(cmd)
	r, _, e := createProcessW.Call(0, uintptr(unsafe.Pointer(&line[0])), 0, 0, 0, 0x08000000 /* CREATE_NO_WINDOW */, 0, 0,
		uintptr(unsafe.Pointer(&si)), uintptr(unsafe.Pointer(&pi)))
	if r == 0 {
		return 0, 0, e
	}
	defer closeHandle.Call(pi.Thread)
	defer closeHandle.Call(pi.Process)
	exit = 999
	if wait > 0 {
		waitSingle.Call(pi.Process, uintptr(wait.Milliseconds()))
		getExitCode.Call(pi.Process, uintptr(unsafe.Pointer(&exit)))
	}
	return int(pi.ProcessID), exit, nil
}

func main() {
	h.Setup("hidden-desktop-spike")
	const desk = "aos-agent"
	hd, _, e := createDesktopW.Call(uintptr(unsafe.Pointer(u16(desk))), 0, 0, 0, 0x10000000|0x01FF /* GENERIC_ALL | DESKTOP_ALL */, 0)
	if hd == 0 {
		fmt.Println("FAIL CreateDesktop:", e)
		os.Exit(1)
	}
	fmt.Println(`created desktop WinSta0\` + desk)
	fg0 := h.Fg()
	out := filepath.Join(h.Dir, "hd-out.txt")
	_ = os.WriteFile(out, nil, 0o644)
	formPid, _, err := run(`WinSta0\`+desk, `"`+filepath.Join(h.Dir, "testform.exe")+`" hidden-form`, 0)
	fmt.Printf("form pid on hidden desktop: %d (%v)\n", formPid, err)
	time.Sleep(4 * time.Second)
	onDesk := func(label, args string) {
		f, _ := os.OpenFile(out, os.O_APPEND|os.O_WRONLY, 0o644)
		fmt.Fprintln(f, "--- "+label)
		f.Close()
		run(`WinSta0\`+desk, fmt.Sprintf(`cmd.exe /c ""%s" %s >> "%s" 2>&1"`, h.AOS, args, out), 20*time.Second)
	}
	onDesk("window list (run on the hidden desktop)", "window list")
	onDesk("key type", `key type --title=hidden-form " typed-on-hidden-desktop" --json`)
	onDesk("mouse click --find", "mouse click --title=hidden-form --find=ClickMe")
	onDesk("window text", "window text --title=hidden-form")
	onDesk("screenshot of the hidden desktop", `capture screenshot fullscreen --out=`+filepath.Join(h.Dir, "hd-shot.png"))
	fg1 := h.Fg()
	data, _ := os.ReadFile(out)
	for _, l := range strings.Split(string(data), "\n") {
		fmt.Println("  " + strings.TrimRight(l, "\r"))
	}
	state := "UNCHANGED"
	if fg0 != fg1 {
		state = "CHANGED"
	}
	fmt.Printf("owner foreground before: %s after: %s -> %s\n", fg0, fg1, state)
	list, _ := h.Aos("window", "list")
	fmt.Printf("owner desktop window list has hidden-form: %v\n", strings.Contains(list, "hidden-form"))
	if formPid > 0 {
		h.KillTree(formPid)
	}
	time.Sleep(time.Second)
	r, _, _ := closeDesktop.Call(hd)
	fmt.Printf("CloseDesktop: %v\n", r != 0)
}
