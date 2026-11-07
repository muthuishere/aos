package groups

import (
	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// pactl talks to PulseAudio and to PipeWire's pulse server alike.
func listAudioDevices() ([]audioDevice, error) {
	if !sys.Has("pactl") {
		return nil, &cli.ExitError{Code: 2, Message: "audio devices need `pactl` (PulseAudio or pipewire-pulse)"}
	}
	var out []audioDevice
	for _, k := range []struct{ kind, dir, def string }{{"sinks", "output", "get-default-sink"}, {"sources", "input", "get-default-source"}} {
		def, _ := sys.Output("pactl", k.def)
		text, err := sys.Output("pactl", "list", k.kind)
		if err != nil {
			return nil, err
		}
		out = append(out, parsePactlList(text, k.dir, def)...)
	}
	return out, nil
}

func setDefaultAudioDevice(d audioDevice) error {
	verb := "set-default-sink"
	if d.Direction == "input" {
		verb = "set-default-source"
	}
	_, err := sys.Output("pactl", verb, d.ID)
	return err
}
