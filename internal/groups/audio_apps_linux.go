package groups

import (
	"strconv"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// Per-app streams are PulseAudio sink inputs; pipewire-pulse serves them too.
func listAudioApps() ([]audioApp, error) {
	if !sys.Has("pactl") {
		return nil, &cli.ExitError{Code: 2, Message: "unsupported: per-app audio needs `pactl` (PulseAudio or pipewire-pulse)"}
	}
	text, err := sys.Output("pactl", "list", "sink-inputs")
	if err != nil {
		return nil, err
	}
	return parsePactlSinkInputs(text), nil
}

func setAudioApps(targets []audioApp, volume, mute int) error {
	for _, a := range targets {
		var err error
		if volume >= 0 {
			_, err = sys.Output("pactl", "set-sink-input-volume", a.ID, strconv.Itoa(volume)+"%")
		} else {
			_, err = sys.Output("pactl", "set-sink-input-mute", a.ID, strconv.Itoa(mute))
		}
		if err != nil {
			return err
		}
	}
	return nil
}
