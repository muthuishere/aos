package groups

import (
	"strings"
	"testing"
)

func TestMatchAudioDevice(t *testing.T) {
	devs := []audioDevice{
		{ID: "BuiltInSpeakerDevice", Name: "MacBook Pro Speakers", Direction: "output"},
		{ID: "AirPods-uid", Name: "Muthu's AirPods Pro", Direction: "output"},
		{ID: "AirPods-uid", Name: "Muthu's AirPods Pro", Direction: "input"},
		{ID: "dell", Name: "DELL U2723QE", Direction: "output"},
		{ID: "dell2", Name: "DELL U2723QE (2)", Direction: "output"},
	}
	cases := map[string]string{
		"airpods":              "AirPods-uid",
		"BuiltInSpeakerDevice": "BuiltInSpeakerDevice",
		"dell u2723qe":         "dell", // exact name beats the longer substring hit
	}
	for q, want := range cases {
		d, err := matchAudioDevice(devs, "output", q)
		if err != nil || d.ID != want {
			t.Errorf("match(%q) = %+v, %v; want %s", q, d, err, want)
		}
	}
	if d, err := matchAudioDevice(devs, "input", "airpods"); err != nil || d.Direction != "input" {
		t.Errorf("input match = %+v, %v", d, err)
	}
	if _, err := matchAudioDevice(devs, "output", "u2723"); err == nil || !strings.Contains(err.Error(), "matches 2") {
		t.Errorf("ambiguous should be refused: %v", err)
	}
	if _, err := matchAudioDevice(devs, "input", "speakers"); err == nil {
		t.Error("output-only device must not match as input")
	}
}

func TestParsePactlList(t *testing.T) {
	text := `Sink #0
	State: SUSPENDED
	Name: alsa_output.pci.analog-stereo
	Description: Built-in Audio Analog Stereo
	Properties:
		device.description = "x"
Sink #1
	Name: bluez_output.AA_BB.1
	Description: WH-1000XM4
Source #2
	Name: alsa_output.pci.analog-stereo.monitor
	Description: Monitor of Built-in Audio
`
	got := parsePactlList(text, "output", "bluez_output.AA_BB.1\n")
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Name != "Built-in Audio Analog Stereo" || got[0].Default {
		t.Errorf("first = %+v", got[0])
	}
	if !got[1].Default || got[1].ID != "bluez_output.AA_BB.1" {
		t.Errorf("default = %+v", got[1])
	}
}

func TestListAudioDevicesLive(t *testing.T) {
	devs, err := listAudioDevices()
	if err != nil {
		t.Skipf("no audio devices here: %v", err)
	}
	defaults := 0
	for _, d := range devs {
		if d.ID == "" || (d.Direction != "output" && d.Direction != "input") {
			t.Errorf("bad device %+v", d)
		}
		if d.Default {
			defaults++
		}
	}
	if len(devs) > 0 && defaults == 0 {
		t.Error("no default device marked")
	}
}
