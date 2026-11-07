package groups

import (
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const personalizeKey = `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`

const (
	hwndBroadcast   = 0xFFFF
	wmSettingChange = 0x001A
	smtoAbortIfHung = 0x0002
)

var procSendMessageTimeout = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

// getDarkMode reads the apps theme, which is what "dark mode" means to most
// people; a missing value is Windows' default, light.
func getDarkMode() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, personalizeKey, registry.QUERY_VALUE)
	if err != nil {
		return false, nil
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue("AppsUseLightTheme")
	if err != nil {
		return false, nil
	}
	return v == 0, nil
}

// setDarkMode flips both the apps and the system (taskbar, Start) theme,
// then tells running windows the colour set changed so they repaint, which
// is what the Settings app does.
func setDarkMode(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, personalizeKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	light := uint32(1)
	if on {
		light = 0
	}
	if err := k.SetDWordValue("AppsUseLightTheme", light); err != nil {
		return err
	}
	if err := k.SetDWordValue("SystemUsesLightTheme", light); err != nil {
		return err
	}
	param, _ := windows.UTF16PtrFromString("ImmersiveColorSet")
	var result uintptr
	procSendMessageTimeout.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(param)),
		smtoAbortIfHung, 2000, uintptr(unsafe.Pointer(&result)))
	return nil
}

// Night light lives in an undocumented binary blob under CloudStore with no
// API to change it, so aos does not guess at it.
var errNoNightLight = &cli.ExitError{Code: 2, Message: "Night light has no supported API on Windows; use Settings > System > Display"}

func getNightLight() (bool, error) { return false, errNoNightLight }
func setNightLight(bool) error     { return errNoNightLight }
