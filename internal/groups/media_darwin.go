package groups

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework ApplicationServices
#import <AppKit/AppKit.h>

// aosMediaKey presses and releases a hardware media key (NX_KEYTYPE_*), the
// same event the keyboard's play / next / previous keys send, so it reaches
// whichever app owns Now Playing.
static void aosMediaKey(int key) {
	for (int down = 1; down >= 0; down--) {
		NSEvent *e = [NSEvent otherEventWithType:NSEventTypeSystemDefined
			location:NSZeroPoint
			modifierFlags:(down ? 0xa00 : 0xb00)
			timestamp:0 windowNumber:0 context:nil subtype:8
			data1:((key << 16) | ((down ? 0xa : 0xb) << 8))
			data2:-1];
		CGEventPost(kCGHIDEventTap, [e CGEvent]);
	}
}
*/
import "C"

import (
	"github.com/muthuishere/aos/internal/sys"
)

const (
	nxKeyPlay     = 16
	nxKeyNext     = 17
	nxKeyPrevious = 18
)

// Since macOS 15.4 the system Now Playing data (MediaRemote) is closed to
// unentitled apps, so aos asks the two players that answer AppleScript.
// Each script checks `is running` first: telling a closed app would launch it.
var nowPlayingScripts = []struct {
	app    string
	scale  float64 // Spotify reports duration in ms
	script string
}{
	{"Music", 1, `if application "Music" is running then
	tell application "Music"
		if player state is stopped then return ""
		set t to current track
		return (player state as text) & tab & (name of t) & tab & (artist of t) & tab & (album of t) & tab & (player position as text) & tab & (duration of t as text)
	end tell
end if
return ""`},
	{"Spotify", 1000, `if application "Spotify" is running then
	tell application "Spotify"
		if player state is stopped then return ""
		set t to current track
		return (player state as text) & tab & (name of t) & tab & (artist of t) & tab & (album of t) & tab & (player position as text) & tab & (duration of t as text)
	end tell
end if
return ""`},
}

func readNowPlaying() (*nowPlaying, error) {
	var cands []*nowPlaying
	for _, p := range nowPlayingScripts {
		out, err := sys.Osascript(p.script)
		if err != nil || out == "" {
			continue
		}
		cands = append(cands, parseNowPlayingLine(p.app, out, p.scale))
	}
	return pickNowPlaying(cands), nil
}

func mediaControl(action string) error {
	switch action {
	case "next":
		C.aosMediaKey(nxKeyNext)
	case "prev":
		C.aosMediaKey(nxKeyPrevious)
	default:
		// The keyboard has one play/pause key; know the state to make
		// play and pause idempotent where a player reports it.
		state := ""
		if np, _ := readNowPlaying(); np != nil {
			state = np.State
		}
		if toggleNeeded(action, state) {
			C.aosMediaKey(nxKeyPlay)
		}
	}
	return nil
}
