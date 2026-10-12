//go:build windows

// automation.go — screenshot + synthetic input for the Windows adapter.
//
// Capture is a classic GDI BitBlt from the screen DC into a 32bpp
// top-down DIB section, encoded to PNG with the stdlib. Input goes
// through SendInput (mouse buttons, unicode text, VK chords) and
// SetCursorPos. Coordinates are virtual-screen points in the same
// space GetWindowRect / GetMonitorInfoW report, so screenshots, window
// bounds and clicks all line up without a scale factor.
package windows

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine/core"
	"golang.org/x/sys/windows"
)

var (
	gdi32                  = windows.NewLazySystemDLL("gdi32.dll")
	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procSetCursorPos       = user32.NewProc("SetCursorPos")
	procSendInput          = user32.NewProc("SendInput")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procBitBlt             = gdi32.NewProc("BitBlt")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

const (
	srcCopy    = 0x00CC0020
	captureBlt = 0x40000000
)

func (a *Adapter) CaptureRect(bounds core.Rect, outPath string) error {
	screenDC, _, _ := procGetDC.Call(0)
	if screenDC == 0 {
		return fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	// Negative height = top-down DIB, so rows read out in image order.
	bmi := bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    int32(bounds.W),
		Height:   -int32(bounds.H),
		Planes:   1,
		BitCount: 32,
	}
	var bits unsafe.Pointer
	bitmap, _, _ := procCreateDIBSection.Call(
		memDC, uintptr(unsafe.Pointer(&bmi)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return fmt.Errorf("CreateDIBSection failed")
	}
	defer procDeleteObject.Call(bitmap)

	prev, _, _ := procSelectObject.Call(memDC, bitmap)
	defer procSelectObject.Call(memDC, prev)

	ok, _, err := procBitBlt.Call(
		memDC, 0, 0, uintptr(bounds.W), uintptr(bounds.H),
		screenDC, uintptr(bounds.X), uintptr(bounds.Y),
		srcCopy|captureBlt)
	if ok == 0 {
		return fmt.Errorf("BitBlt failed: %v", err)
	}

	n := bounds.W * bounds.H * 4
	bgra := unsafe.Slice((*byte)(bits), n)
	img := image.NewRGBA(image.Rect(0, 0, bounds.W, bounds.H))
	for i := 0; i < n; i += 4 {
		img.Pix[i] = bgra[i+2]   // R
		img.Pix[i+1] = bgra[i+1] // G
		img.Pix[i+2] = bgra[i]   // B
		img.Pix[i+3] = 0xFF      // BitBlt alpha is garbage; force opaque
	}

	f, ferr := os.Create(outPath)
	if ferr != nil {
		return fmt.Errorf("create %s: %w", outPath, ferr)
	}
	defer f.Close()
	if perr := png.Encode(f, img); perr != nil {
		return fmt.Errorf("encode png: %w", perr)
	}
	return nil
}

// input mirrors the Win32 INPUT struct for INPUT_MOUSE. The union
// slot is sized by MOUSEINPUT (32 bytes), the largest member; the
// keyboard variant below pads itself up to the same 40-byte total so
// SendInput's cbSize check passes for both.
type mouseEventInput struct {
	Type      uint32
	_         uint32 // union alignment padding on 64-bit
	Dx        int32
	Dy        int32
	MouseData uint32
	DwFlags   uint32
	Time      uint32
	_         uint32 // ULONG_PTR alignment
	ExtraInfo uintptr
}

type keyboardEventInput struct {
	Type      uint32
	_         uint32
	WVk       uint16
	WScan     uint16
	DwFlags   uint32
	Time      uint32
	_         uint32
	ExtraInfo uintptr
	_         [8]byte // pad KEYBDINPUT (24) up to MOUSEINPUT (32)
}

const (
	inputMouse    = 0
	inputKeyboard = 1

	mouseeventfLeftDown   = 0x0002
	mouseeventfLeftUp     = 0x0004
	mouseeventfRightDown  = 0x0008
	mouseeventfRightUp    = 0x0010
	mouseeventfMiddleDown = 0x0020
	mouseeventfMiddleUp   = 0x0040

	keyeventfKeyUp   = 0x0002
	keyeventfUnicode = 0x0004
)

func sendMouseInputs(events []mouseEventInput) error {
	sent, _, err := procSendInput.Call(
		uintptr(len(events)),
		uintptr(unsafe.Pointer(&events[0])),
		unsafe.Sizeof(events[0]))
	if int(sent) != len(events) {
		return fmt.Errorf("SendInput sent %d/%d mouse events: %v", sent, len(events), err)
	}
	return nil
}

func sendKeyboardInputs(events []keyboardEventInput) error {
	sent, _, err := procSendInput.Call(
		uintptr(len(events)),
		uintptr(unsafe.Pointer(&events[0])),
		unsafe.Sizeof(events[0]))
	if int(sent) != len(events) {
		return fmt.Errorf("SendInput sent %d/%d key events: %v", sent, len(events), err)
	}
	return nil
}

func (a *Adapter) MouseMove(x, y int) error {
	ok, _, err := procSetCursorPos.Call(uintptr(x), uintptr(y))
	if ok == 0 {
		return fmt.Errorf("SetCursorPos(%d,%d) failed: %v", x, y, err)
	}
	return nil
}

func (a *Adapter) MouseClick(x, y int, button core.MouseButton, clicks int) error {
	if err := a.MouseMove(x, y); err != nil {
		return err
	}
	var down, up uint32
	switch button {
	case core.MouseRight:
		down, up = mouseeventfRightDown, mouseeventfRightUp
	case core.MouseMiddle:
		down, up = mouseeventfMiddleDown, mouseeventfMiddleUp
	default:
		down, up = mouseeventfLeftDown, mouseeventfLeftUp
	}
	for i := 0; i < clicks; i++ {
		err := sendMouseInputs([]mouseEventInput{
			{Type: inputMouse, DwFlags: down},
			{Type: inputMouse, DwFlags: up},
		})
		if err != nil {
			return err
		}
		if i < clicks-1 {
			time.Sleep(60 * time.Millisecond)
		}
	}
	return nil
}

func (a *Adapter) CursorPosition() (int, int, error) {
	var p point
	ok, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		return 0, 0, fmt.Errorf("GetCursorPos failed: %v", err)
	}
	return int(p.X), int(p.Y), nil
}

// typeKeyDelay paces injected keystrokes.
//
// Sending a whole string as one SendInput batch is accepted by the OS and then
// mangled by the target: WinUI and Chromium apps process injected input on
// their own thread, and a burst arrives faster than that thread drains it, so
// characters are dropped or repeated. Typing "hello from agentic-os" into
// Windows 11 Notepad produced "hello sssssssssssssss" — the right length, with
// every character after the first word replaced by the last one.
//
// The other platforms already pace: the Linux adapter passes `--delay 12` to
// xdotool. 5ms still dropped and repeated characters in Win11 Notepad on arm64; 20ms is exact.
const typeKeyDelay = 20 * time.Millisecond

func (a *Adapter) TypeText(text string) error {
	units := windows.StringToUTF16(text)
	units = units[:len(units)-1] // drop the NUL terminator
	if len(units) == 0 {
		return nil
	}

	// One character per call — a down/up pair — so the target's input queue is
	// never handed more than it can drain.
	for i, u := range units {
		events := []keyboardEventInput{
			{Type: inputKeyboard, WScan: u, DwFlags: keyeventfUnicode},
			{Type: inputKeyboard, WScan: u, DwFlags: keyeventfUnicode | keyeventfKeyUp},
		}
		if err := sendKeyboardInputs(events); err != nil {
			return fmt.Errorf("typing %q at character %d: %w", text, i+1, err)
		}
		time.Sleep(typeKeyDelay)
	}
	return nil
}

// windowsVKCodes maps normalized key names to Win32 virtual-key codes.
// Letters/digits are handled arithmetically in PressChord; this table
// covers the named keys and OEM punctuation (US layout positions).
var windowsVKCodes = map[string]uint16{
	"enter": 0x0D, "tab": 0x09, "esc": 0x1B, "space": 0x20,
	"left": 0x25, "up": 0x26, "right": 0x27, "down": 0x28,
	"delete": 0x2E, "backspace": 0x08,
	"home": 0x24, "end": 0x23, "pageup": 0x21, "pagedown": 0x22,
	"f1": 0x70, "f2": 0x71, "f3": 0x72, "f4": 0x73, "f5": 0x74,
	"f6": 0x75, "f7": 0x76, "f8": 0x77, "f9": 0x78, "f10": 0x79,
	"f11": 0x7A, "f12": 0x7B,
	";": 0xBA, "=": 0xBB, ",": 0xBC, "-": 0xBD, ".": 0xBE,
	"/": 0xBF, "`": 0xC0, "[": 0xDB, "\\": 0xDC, "]": 0xDD, "'": 0xDE,
}

const (
	vkShift   = 0x10
	vkControl = 0x11
	vkMenu    = 0x12 // Alt
	vkLWin    = 0x5B
)

func vkForKey(key string) (uint16, error) {
	if len(key) == 1 {
		c := key[0]
		if c >= 'a' && c <= 'z' {
			return uint16(c - 'a' + 0x41), nil
		}
		if c >= '0' && c <= '9' {
			return uint16(c - '0' + 0x30), nil
		}
	}
	if vk, ok := windowsVKCodes[key]; ok {
		return vk, nil
	}
	return 0, fmt.Errorf("key %q has no Windows virtual-key mapping", key)
}

func (a *Adapter) PressChord(chord core.Chord) error {
	vk, err := vkForKey(chord.Key)
	if err != nil {
		return err
	}
	var mods []uint16
	if chord.Cmd {
		mods = append(mods, vkLWin)
	}
	if chord.Ctrl {
		mods = append(mods, vkControl)
	}
	if chord.Alt {
		mods = append(mods, vkMenu)
	}
	if chord.Shift {
		mods = append(mods, vkShift)
	}
	var events []keyboardEventInput
	for _, m := range mods {
		events = append(events, keyboardEventInput{Type: inputKeyboard, WVk: m})
	}
	events = append(events,
		keyboardEventInput{Type: inputKeyboard, WVk: vk},
		keyboardEventInput{Type: inputKeyboard, WVk: vk, DwFlags: keyeventfKeyUp},
	)
	for i := len(mods) - 1; i >= 0; i-- {
		events = append(events, keyboardEventInput{Type: inputKeyboard, WVk: mods[i], DwFlags: keyeventfKeyUp})
	}
	return sendKeyboardInputs(events)
}

func (a *Adapter) Launch(app string) error {
	// `start` resolves App Paths registrations and PATH alike; the
	// empty first argument is start's window-title slot.
	if err := exec.Command("cmd", "/c", "start", "", app).Run(); err != nil {
		return fmt.Errorf("launch %q: %w", app, err)
	}
	return nil
}

// Windows has no Screen Recording-style TCC gate for GDI capture.
func (a *Adapter) CheckScreenCapture() bool    { return true }
func (a *Adapter) RequestScreenCapture() error { return nil }
