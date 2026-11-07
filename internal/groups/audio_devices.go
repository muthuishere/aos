package groups

import (
	"fmt"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// audioDevice is one sound device in one direction, in the shape
// `audio devices --json` emits. A headset that both plays and records is
// listed twice, once as output and once as input.
type audioDevice struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Direction string `json:"direction"` // output or input
	Default   bool   `json:"default"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "audio", Name: "devices",
				Summary: "List output and input sound devices, marking the defaults",
				Args:    "[--json]",
				Examples: []string{
					"aos audio devices",
					"aos audio devices --json",
				},
				Run: runAudioDevices,
			},
			&cli.Command{
				Group: "audio", Name: "output",
				Summary: "Print the default output device, or switch it by name or id",
				Args:    "[<device>]",
				Examples: []string{
					"aos audio output",
					"aos audio output AirPods",
					`aos audio output "MacBook Pro Speakers"`,
				},
				Run: func(c *cli.Ctx, args []string) error { return runAudioSwitch(c, "output", args) },
			},
			&cli.Command{
				Group: "audio", Name: "input",
				Summary: "Print the default input device, or switch it by name or id",
				Args:    "[<device>]",
				Examples: []string{
					"aos audio input",
					`aos audio input "MacBook Pro Microphone"`,
				},
				Run: func(c *cli.Ctx, args []string) error { return runAudioSwitch(c, "input", args) },
			},
		)
	})
}

func runAudioDevices(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("audio devices", args)
	if err != nil {
		return err
	}
	devs, err := listAudioDevices()
	if err != nil {
		return err
	}
	if asJSON {
		if devs == nil {
			devs = []audioDevice{}
		}
		return writeJSON(c, devs)
	}
	for _, d := range devs {
		mark := " "
		if d.Default {
			mark = "*"
		}
		c.Printf("%s %-6s  %s\n", mark, d.Direction, d.Name)
	}
	return nil
}

func runAudioSwitch(c *cli.Ctx, direction string, args []string) error {
	devs, err := listAudioDevices()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		for _, d := range devs {
			if d.Direction == direction && d.Default {
				c.Println(d.Name)
				return nil
			}
		}
		return &cli.ExitError{Code: 1, Message: "no default " + direction + " device"}
	}
	d, err := matchAudioDevice(devs, direction, strings.Join(args, " "))
	if err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	if err := setDefaultAudioDevice(d); err != nil {
		return err
	}
	c.Printf("%s: %s\n", direction, d.Name)
	return nil
}

// matchAudioDevice resolves what a person typed to one device: an exact id,
// then an exact name (ignoring case), then a name containing the text. More
// than one substring hit is refused with the candidates, never guessed.
func matchAudioDevice(all []audioDevice, direction, query string) (audioDevice, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var exact, partial []audioDevice
	for _, d := range all {
		if d.Direction != direction {
			continue
		}
		if d.ID == query {
			return d, nil
		}
		name := strings.ToLower(d.Name)
		if name == q {
			exact = append(exact, d)
		} else if strings.Contains(name, q) {
			partial = append(partial, d)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}
	cands := exact
	if len(cands) == 0 {
		cands = partial
	}
	switch len(cands) {
	case 0:
		return audioDevice{}, fmt.Errorf("no %s device matching %q; see `aos audio devices`", direction, query)
	case 1:
		return cands[0], nil
	}
	names := make([]string, len(cands))
	for i, d := range cands {
		names[i] = "  " + d.Name
	}
	return audioDevice{}, fmt.Errorf("%q matches %d %s devices; be more specific:\n%s", query, len(cands), direction, strings.Join(names, "\n"))
}

// parsePactlList reads `pactl list sinks` or `pactl list sources` into
// devices, skipping the monitor sources PulseAudio makes for every sink.
func parsePactlList(text, direction, defaultName string) []audioDevice {
	var out []audioDevice
	var cur *audioDevice
	flush := func() {
		if cur != nil && cur.ID != "" && !strings.HasSuffix(cur.ID, ".monitor") {
			if cur.Name == "" {
				cur.Name = cur.ID
			}
			cur.Default = cur.ID == strings.TrimSpace(defaultName)
			out = append(out, *cur)
		}
		cur = nil
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Sink #") || strings.HasPrefix(line, "Source #") {
			flush()
			cur = &audioDevice{Direction: direction}
			continue
		}
		if cur == nil {
			continue
		}
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "Name: "); ok && cur.ID == "" {
			cur.ID = v
		} else if v, ok := strings.CutPrefix(t, "Description: "); ok && cur.Name == "" {
			cur.Name = v
		}
	}
	flush()
	return out
}
