package groups

import (
	"os"
	"os/exec"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows"
)

// Windows notifications go through the notification-area balloon
// (Shell_NotifyIcon with NIF_INFO), which Windows 10 and 11 show as a native
// toast. A toast through WinRT would need a registered app id; the balloon
// needs nothing. The icon has to exist while the toast shows, so a detached
// aos child (`notify hold`) owns it for a few seconds and `notify` returns at once.

var (
	user32           = windows.NewLazySystemDLL("user32.dll")
	procCreateWindow = user32.NewProc("CreateWindowExW")
	procDestroyWin   = user32.NewProc("DestroyWindow")
	procLoadIcon     = user32.NewProc("LoadIconW")
	procShellNotify  = windows.NewLazySystemDLL("shell32.dll").NewProc("Shell_NotifyIconW")
)

// notifyIconData is NOTIFYICONDATAW.
type notifyIconData struct {
	CbSize           uint32
	HWnd             windows.Handle
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            windows.Handle
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UTimeout         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GUIDItem         windows.GUID
	HBalloonIcon     windows.Handle
}

const (
	nimAdd         = 0
	nimDelete      = 2
	nifIcon        = 0x2
	nifTip         = 0x4
	nifInfo        = 0x10
	niifInfo       = 0x1
	niifNoSound    = 0x10
	idiInformation = 32516
	notifyShowFor  = 8 * time.Second
)

func postNotification(title, body string, sound bool) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args := []string{"notify", "hold", title, body}
	if sound {
		args = append(args, "--sound")
	}
	cmd := exec.Command(exe, args...)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func runNotifyHold(_ *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	title, body := "", ""
	if len(set.Rest) > 0 {
		title = set.Rest[0]
	}
	if len(set.Rest) > 1 {
		body = set.Rest[1]
	}
	if body == "" {
		body = " " // a balloon with an empty body is not shown
	}
	static, _ := windows.UTF16PtrFromString("STATIC")
	hwndMessage := ^uintptr(2) // HWND_MESSAGE (-3)
	hwnd, _, err := procCreateWindow.Call(0, uintptr(unsafe.Pointer(static)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
	if hwnd == 0 {
		return err
	}
	defer procDestroyWin.Call(hwnd)
	icon, _, _ := procLoadIcon.Call(0, idiInformation)

	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = windows.Handle(hwnd)
	nid.UID = 1
	nid.UFlags = nifIcon | nifTip | nifInfo
	nid.HIcon = windows.Handle(icon)
	nid.DwInfoFlags = niifInfo
	if !set.Has("sound") {
		nid.DwInfoFlags |= niifNoSound
	}
	copyUTF16(nid.SzTip[:], "aos")
	copyUTF16(nid.SzInfoTitle[:], title)
	copyUTF16(nid.SzInfo[:], body)
	if r, _, err := procShellNotify.Call(nimAdd, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return err
	}
	time.Sleep(notifyShowFor)
	procShellNotify.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	return nil
}

// copyUTF16 writes s into a fixed NUL-terminated buffer, truncating.
func copyUTF16(dst []uint16, s string) {
	u := windows.StringToUTF16(s)
	if len(u) > len(dst) {
		u = append(u[:len(dst)-1], 0)
	}
	copy(dst, u)
}
