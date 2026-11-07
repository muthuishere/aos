package groups

import (
	"unsafe"

	"github.com/muthuishere/aos/internal/cli"
	"golang.org/x/sys/windows"
)

var procMediaSendInput = windows.NewLazySystemDLL("user32.dll").NewProc("SendInput")

const (
	vkMediaNext      = 0xB0
	vkMediaPrev      = 0xB1
	vkMediaPlayPause = 0xB3
	keyeventfKeyUp   = 0x0002
	inputKeyboard    = 1
)

// mediaKeyInput is INPUT with a KEYBDINPUT, padded to the union's size.
type mediaKeyInput struct {
	Type      uint32
	_         uint32
	Vk        uint16
	Scan      uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
	_         [8]byte
}

// The current track lives in WinRT's GlobalSystemMediaTransportControls,
// which Go cannot reach without a WinRT projection; aos keeps that out.
func readNowPlaying() (*nowPlaying, error) {
	return nil, &cli.ExitError{Code: 2, Message: "`media now` is not available on Windows yet (the track lives in WinRT's media session API); play, pause, next and prev work"}
}

// mediaControl sends the keyboard's media keys, which Windows routes to the
// app holding the media session. Windows has one play/pause key and aos
// cannot read the state here, so play and pause both press it.
func mediaControl(action string) error {
	vk := map[string]uint16{"play": vkMediaPlayPause, "pause": vkMediaPlayPause, "toggle": vkMediaPlayPause, "next": vkMediaNext, "prev": vkMediaPrev}[action]
	in := []mediaKeyInput{
		{Type: inputKeyboard, Vk: vk},
		{Type: inputKeyboard, Vk: vk, Flags: keyeventfKeyUp},
	}
	n, _, err := procMediaSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if n != uintptr(len(in)) {
		return err
	}
	return nil
}
