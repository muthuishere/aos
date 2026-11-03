//go:build windows

package windows

// Focus-free input to terminals: Windows Terminal tabs and classic console
// (conhost) windows.
//
// A terminal window has no text control, and posted WM_CHAR does not reach
// the shell. But every shell has a console input buffer, and any process may
// attach to another process's console (AttachConsole) and write key events
// into that buffer (WriteConsoleInputW). The shell reads them as typed keys
// while its window stays in the background; this holds for ConPTY too
// (Windows Terminal's OpenConsole keeps a real input buffer per tab).
// Reading the screen goes the same way (ReadConsoleOutputCharacterW).
//
// A process has at most one console, and attaching drops aos's own. So the
// attach runs in a short-lived helper: aos.exe re-executed with no console
// (DETACHED_PROCESS) and ConsoleHelperArg as its first argument. The caller's
// console and output are never touched.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

// ConsoleHelperArg is argv[1] of the console helper process.
const ConsoleHelperArg = "__aos-console-helper"

var (
	procAttachConsole           = kernel32.NewProc("AttachConsole")
	procFreeConsole             = kernel32.NewProc("FreeConsole")
	procWriteConsoleInputW      = kernel32.NewProc("WriteConsoleInputW")
	procReadConsoleOutputCharW  = kernel32.NewProc("ReadConsoleOutputCharacterW")
	procGetConsoleTitleW        = kernel32.NewProc("GetConsoleTitleW")
	procGetConsoleScreenBufInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
	procGetConsoleProcessList   = kernel32.NewProc("GetConsoleProcessList")
	consoleWindowClasses        = []string{"ConsoleWindowClass", "CASCADIA_HOSTING_WINDOW_CLASS", "PseudoConsoleWindow"}
	consoleHostImages           = map[string]bool{"openconsole.exe": true, "conhost.exe": true, "windowsterminal.exe": true}
	errNotConsole               = errors.New("not a terminal window")
)

func isConsoleWindow(top uintptr) bool {
	c := className(top)
	for _, k := range consoleWindowClasses {
		if strings.EqualFold(c, k) {
			return true
		}
	}
	return false
}

// keyRecord is INPUT_RECORD with a KEY_EVENT_RECORD (20 bytes).
type keyRecord struct {
	EventType uint16
	_         uint16
	KeyDown   int32
	Repeat    uint16
	VK        uint16
	Scan      uint16
	Char      uint16
	CtrlState uint32
}

const (
	keyEvent        = 0x0001
	leftCtrlPressed = 0x0008
	leftAltPressed  = 0x0002
	shiftPressed    = 0x0010
	vkReturn        = 0x0D
)

type procInfo struct {
	pid, parent uint32
	exe         string
}

func processTable() ([]procInfo, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []procInfo
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		out = append(out, procInfo{e.ProcessID, e.ParentProcessID, strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))})
	}
	return out, nil
}

// consoleCandidates are the processes whose console may be this window's:
// the window's own process and its descendants (WindowsTerminal ->
// OpenConsole -> shell), hosts excluded, shells first.
func consoleCandidates(top uintptr) []uint32 {
	var pid uint32
	procGetWindowThreadPID.Call(top, uintptr(unsafe.Pointer(&pid)))
	procs, err := processTable()
	if err != nil {
		return []uint32{pid}
	}
	kids := map[uint32][]procInfo{}
	byPID := map[uint32]procInfo{}
	for _, p := range procs {
		kids[p.parent] = append(kids[p.parent], p)
		byPID[p.pid] = p
	}
	var out []uint32
	if p, ok := byPID[pid]; !ok || !consoleHostImages[p.exe] {
		out = append(out, pid) // a classic console window reports its client
	}
	seen := map[uint32]bool{pid: true}
	queue := []uint32{pid}
	for len(queue) > 0 && len(out) < 64 {
		cur := queue[0]
		queue = queue[1:]
		for _, k := range kids[cur] {
			if seen[k.pid] {
				continue
			}
			seen[k.pid] = true
			queue = append(queue, k.pid)
			if !consoleHostImages[k.exe] {
				out = append(out, k.pid)
			}
		}
	}
	return out
}

// helperRequest is what the parent sends the helper on stdin.
type helperRequest struct {
	op    string // "type", "key", "read", "title"
	pids  []uint32
	title string
	data  string
}

// runConsoleHelper runs one console operation in a console-less child and
// returns its stdout.
func runConsoleHelper(req helperRequest) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	pids := make([]string, len(req.pids))
	for i, p := range req.pids {
		pids[i] = strconv.Itoa(int(p))
	}
	cmd := exec.Command(exe, ConsoleHelperArg, req.op, strings.Join(pids, ","), req.title)
	cmd.Stdin = strings.NewReader(req.data)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 /* DETACHED_PROCESS */, HideWindow: true}
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return out.String(), nil
}

// ConsoleHelperMain is the helper process: attach to the first candidate
// whose console title matches (else the first that attaches), do the op,
// write the result to stdout. Returns the exit code.
func ConsoleHelperMain(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "console helper: want <op> <pids> [title]")
		return 2
	}
	op := args[0]
	title := ""
	if len(args) > 2 {
		title = strings.ToLower(args[2])
	}
	var pids []uint32
	for _, s := range strings.Split(args[1], ",") {
		if n, err := strconv.Atoi(s); err == nil {
			pids = append(pids, uint32(n))
		}
	}
	data, _ := io.ReadAll(stdin)
	procFreeConsole.Call()
	if op == "titles" { // one line per pid: pid<TAB>console title
		for _, pid := range pids {
			if r, _, _ := procAttachConsole.Call(uintptr(pid)); r != 0 {
				fmt.Fprintf(stdout, "%d\t%s\n", pid, consoleTitle())
				procFreeConsole.Call()
			}
		}
		return 0
	}
	attached := uint32(0)
	for pass := 0; pass < 2 && attached == 0; pass++ {
		for _, pid := range pids {
			if r, _, _ := procAttachConsole.Call(uintptr(pid)); r == 0 {
				continue
			}
			if pass == 0 && title != "" && !strings.Contains(strings.ToLower(consoleTitle()), title) {
				procFreeConsole.Call()
				continue
			}
			attached = pid
			break
		}
	}
	if attached == 0 {
		fmt.Fprintln(stderr, "no process of this window has a console to attach to")
		return 1
	}
	defer procFreeConsole.Call()
	switch op {
	case "title":
		fmt.Fprint(stdout, consoleTitle())
		return 0
	case "read":
		s, err := readConsoleScreen()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprint(stdout, s)
		return 0
	case "type", "key":
		in, err := windows.CreateFile(windows.StringToUTF16Ptr("CONIN$"), windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			fmt.Fprintln(stderr, "open CONIN$:", err)
			return 1
		}
		defer windows.CloseHandle(in)
		var recs []keyRecord
		if op == "type" {
			recs = textRecords(string(data))
		} else {
			parts := strings.Split(string(data), "|") // vk|ctrlstate|char
			if len(parts) != 3 {
				fmt.Fprintln(stderr, "bad key spec")
				return 2
			}
			vk, _ := strconv.Atoi(parts[0])
			st, _ := strconv.Atoi(parts[1])
			ch, _ := strconv.Atoi(parts[2])
			recs = []keyRecord{
				{EventType: keyEvent, KeyDown: 1, Repeat: 1, VK: uint16(vk), Char: uint16(ch), CtrlState: uint32(st)},
				{EventType: keyEvent, KeyDown: 0, Repeat: 1, VK: uint16(vk), Char: uint16(ch), CtrlState: uint32(st)},
			}
		}
		for len(recs) > 0 {
			n := min(len(recs), 512)
			var wrote uint32
			if r, _, err := procWriteConsoleInputW.Call(uintptr(in), uintptr(unsafe.Pointer(&recs[0])), uintptr(n), uintptr(unsafe.Pointer(&wrote))); r == 0 {
				fmt.Fprintln(stderr, "WriteConsoleInputW:", err)
				return 1
			}
			recs = recs[wrote:]
		}
		fmt.Fprint(stdout, consoleTitle())
		return 0
	}
	fmt.Fprintln(stderr, "unknown op", op)
	return 2
}

func textRecords(text string) []keyRecord {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var recs []keyRecord
	for _, u := range utf16.Encode([]rune(text)) {
		vk := uint16(0)
		if u == '\n' {
			u, vk = '\r', vkReturn
		}
		recs = append(recs,
			keyRecord{EventType: keyEvent, KeyDown: 1, Repeat: 1, VK: vk, Char: u},
			keyRecord{EventType: keyEvent, KeyDown: 0, Repeat: 1, VK: vk, Char: u})
	}
	return recs
}

func consoleTitle() string {
	buf := make([]uint16, 1024)
	n, _, _ := procGetConsoleTitleW.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return windows.UTF16ToString(buf[:n])
}

type coord struct{ X, Y int16 }
type smallRect struct{ Left, Top, Right, Bottom int16 }
type screenBufInfo struct {
	Size       coord
	Cursor     coord
	Attributes uint16
	Window     smallRect
	MaxSize    coord
}

// readConsoleScreen returns the visible rows of the attached console.
func readConsoleScreen() (string, error) {
	out, err := windows.CreateFile(windows.StringToUTF16Ptr("CONOUT$"), windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return "", fmt.Errorf("open CONOUT$: %w", err)
	}
	defer windows.CloseHandle(out)
	var info screenBufInfo
	if r, _, err := procGetConsoleScreenBufInfo.Call(uintptr(out), uintptr(unsafe.Pointer(&info))); r == 0 {
		return "", fmt.Errorf("GetConsoleScreenBufferInfo: %w", err)
	}
	width := int(info.Size.X)
	var lines []string
	buf := make([]uint16, width)
	for y := info.Window.Top; y <= info.Window.Bottom; y++ {
		var got uint32
		at := uintptr(uint32(uint16(0)) | uint32(uint16(y))<<16)
		procReadConsoleOutputCharW.Call(uintptr(out), uintptr(unsafe.Pointer(&buf[0])), uintptr(width), at, uintptr(unsafe.Pointer(&got)))
		lines = append(lines, strings.TrimRight(windows.UTF16ToString(buf[:got]), " "))
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n"), nil
}

// consoleTypeTo types text into the shell of terminal window top.
func consoleTypeTo(top uintptr, w core.Window, text string) (string, error) {
	if !isConsoleWindow(top) {
		return "", errNotConsole
	}
	title, err := runConsoleHelper(helperRequest{op: "type", pids: consoleCandidates(top), title: w.Title, data: text})
	if err != nil {
		return "", fmt.Errorf("console input: %w", err)
	}
	return fmt.Sprintf("console input buffer of %q (WriteConsoleInputW)", strings.TrimSpace(title)), nil
}

// consoleKeyTo presses one chord in the shell of terminal window top;
// modifier chords work here (the state travels in the key record).
func consoleKeyTo(top uintptr, w core.Window, chord core.Chord) error {
	if !isConsoleWindow(top) {
		return errNotConsole
	}
	vk, state, ch, err := consoleChord(chord)
	if err != nil {
		return err
	}
	_, err = runConsoleHelper(helperRequest{op: "key", pids: consoleCandidates(top), title: w.Title,
		data: fmt.Sprintf("%d|%d|%d", vk, state, ch)})
	if err != nil {
		return fmt.Errorf("console input: %w", err)
	}
	return nil
}

// consoleRead returns the visible screen of terminal window top.
func consoleRead(top uintptr, w core.Window) (string, error) {
	if !isConsoleWindow(top) {
		return "", errNotConsole
	}
	return runConsoleHelper(helperRequest{op: "read", pids: consoleCandidates(top), title: w.Title})
}

var _ = procGetConsoleProcessList

func windowTitle(h uintptr) string {
	n, _, _ := procGetWindowTextLenW.Call(h)
	buf := make([]uint16, n+1)
	procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return windows.UTF16ToString(buf)
}

// ---- terminal tabs: one shell per Windows Terminal tab (pane) ----------

// TerminalShells lists the shells of terminal window w, oldest first: for
// Windows Terminal, every non-host child of the WindowsTerminal process (it
// starts one OpenConsole per pane and the pane's shell beside it, both as
// its own children); for a classic console window, its client. Title is the shell's console title, CWD its current
// directory. WT runs every window in one process, so the list covers all
// of that process's windows; the caller assigns shells to tabs.
func (a *Adapter) TerminalShells(w core.Window) ([]core.ShellInfo, error) {
	top, err := parseHwnd(w.ID)
	if err != nil {
		return nil, err
	}
	if !isConsoleWindow(top) {
		return nil, errNotConsole
	}
	procs, err := processTable()
	if err != nil {
		return nil, err
	}
	kids := map[uint32][]procInfo{}
	for _, p := range procs {
		kids[p.parent] = append(kids[p.parent], p)
	}
	var pid uint32
	procGetWindowThreadPID.Call(top, uintptr(unsafe.Pointer(&pid)))
	// Windows Terminal starts each pane's shell as its own direct child,
	// next to (not under) the pane's OpenConsole.
	var shells []uint32
	for _, c := range kids[pid] {
		if !consoleHostImages[c.exe] {
			shells = append(shells, c.pid)
		}
	}
	if len(shells) == 0 { // a classic console window: its own client
		shells = consoleCandidates(top)
		if len(shells) > 1 {
			shells = shells[:1]
		}
	}
	titles := map[uint32]string{}
	if len(shells) > 0 {
		out, _ := runConsoleHelper(helperRequest{op: "titles", pids: shells})
		for _, line := range strings.Split(out, "\n") {
			if p, t, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t"); ok {
				if n, err := strconv.Atoi(p); err == nil {
					titles[uint32(n)] = t
				}
			}
		}
	}
	out := make([]core.ShellInfo, 0, len(shells))
	for _, p := range shells {
		exe := ""
		for _, pr := range procs {
			if pr.pid == p {
				exe = pr.exe
			}
		}
		out = append(out, core.ShellInfo{PID: int(p), Exe: exe, Title: titles[p], CWD: processCwd(p), Created: processCreated(p)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created < out[j].Created })
	return out, nil
}

// ConsoleType writes text into the console input buffer of shell pid.
func (a *Adapter) ConsoleType(pid int, text string) error {
	_, err := runConsoleHelper(helperRequest{op: "type", pids: []uint32{uint32(pid)}, data: text})
	return err
}

// ConsoleKey presses one chord (modifiers included) in shell pid's console.
func (a *Adapter) ConsoleKey(pid int, chord core.Chord) error {
	vk, state, ch, err := consoleChord(chord)
	if err != nil {
		return err
	}
	_, err = runConsoleHelper(helperRequest{op: "key", pids: []uint32{uint32(pid)}, data: fmt.Sprintf("%d|%d|%d", vk, state, ch)})
	return err
}

// ConsoleRead returns the visible screen of shell pid's console.
func (a *Adapter) ConsoleRead(pid int) (string, error) {
	return runConsoleHelper(helperRequest{op: "read", pids: []uint32{uint32(pid)}})
}

// ConsoleClose ends shell pid and its children with exit code 0, which
// Windows Terminal's default closeOnExit ("automatic") treats as a clean
// exit and closes the tab. No key reaches the shell.
func (a *Adapter) ConsoleClose(pid int) error {
	procs, err := processTable()
	if err != nil {
		return err
	}
	var kill func(p uint32)
	kill = func(p uint32) {
		for _, c := range procs {
			if c.parent == p && c.pid != p {
				kill(c.pid)
			}
		}
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, p); err == nil {
			_ = windows.TerminateProcess(h, 0)
			windows.CloseHandle(h)
		}
	}
	kill(uint32(pid))
	return nil
}

func processCreated(pid uint32) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &e, &k, &u); err != nil {
		return 0
	}
	return c.Nanoseconds()
}

// processCwd reads another process's current directory from its PEB:
// PEB.ProcessParameters (+0x20) -> RTL_USER_PROCESS_PARAMETERS.CurrentDirectory
// .DosPath (+0x38, a UNICODE_STRING). 64-bit layouts (amd64 and arm64).
func processCwd(pid uint32) string {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		return ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	var pbi windows.PROCESS_BASIC_INFORMATION
	var n uint32
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), &n); err != nil {
		return ""
	}
	read := func(addr uintptr, buf unsafe.Pointer, size uintptr) bool {
		var got uintptr
		return windows.ReadProcessMemory(h, addr, (*byte)(buf), size, &got) == nil && got == size
	}
	var params uintptr
	if !read(uintptr(unsafe.Pointer(pbi.PebBaseAddress))+0x20, unsafe.Pointer(&params), 8) {
		return ""
	}
	var us struct {
		Length, MaxLength uint16
		_                 uint32
		Buffer            uintptr
	}
	if !read(params+0x38, unsafe.Pointer(&us), unsafe.Sizeof(us)) || us.Length == 0 || us.Length > 4096 {
		return ""
	}
	buf := make([]uint16, us.Length/2)
	if !read(us.Buffer, unsafe.Pointer(&buf[0]), uintptr(us.Length)) {
		return ""
	}
	return strings.TrimRight(string(utf16.Decode(buf)), `\`)
}

// consoleChord turns a chord into a console key record's vk, control
// state and character.
func consoleChord(chord core.Chord) (vk, state, ch int, err error) {
	v, err := vkForKey(chord.Key)
	if err != nil {
		return 0, 0, 0, err
	}
	vk = int(v)
	if chord.Ctrl || chord.Cmd {
		state |= leftCtrlPressed
	}
	if chord.Alt {
		state |= leftAltPressed
	}
	if chord.Shift {
		state |= shiftPressed
	}
	switch {
	case vk == vkReturn:
		ch = '\r'
	case vk == 0x09:
		ch = '\t'
	case vk == 0x08:
		ch = 8
	case vk == 0x1B:
		ch = 27
	case len(chord.Key) == 1:
		c := strings.ToLower(chord.Key)[0]
		ch = int(c)
		if (chord.Ctrl || chord.Cmd) && c >= 'a' && c <= 'z' {
			ch = int(c-'a') + 1 // ctrl+c -> 0x03
		} else if chord.Shift {
			ch = int(strings.ToUpper(chord.Key)[0])
		}
	}
	return vk, state, ch, nil
}
