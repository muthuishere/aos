package groups

import (
	"strings"
	"testing"
)

func TestParsePactlSinkInputs(t *testing.T) {
	text := `Sink Input #42
	Driver: protocol-native.c
	Corked: no
	Mute: no
	Volume: front-left: 42598 /  65% / -11.23 dB,   front-right: 42598 /  65% / -11.23 dB
	Properties:
		application.name = "Firefox"
		application.process.id = "1234"
		application.process.binary = "firefox"
Sink Input #43
	Corked: yes
	Mute: yes
	Volume: mono: 65536 / 100% / 0.00 dB
	Properties:
		application.process.id = "77"
		application.process.binary = "mpv"
`
	apps := parsePactlSinkInputs(text)
	if len(apps) != 2 {
		t.Fatalf("got %d apps: %+v", len(apps), apps)
	}
	a, b := apps[0], apps[1]
	if a.ID != "42" || a.Name != "Firefox" || a.PID != 1234 || !a.Playing || *a.Volume != 65 || *a.Muted {
		t.Errorf("first = %+v", a)
	}
	if b.Name != "mpv" || b.Playing || *b.Volume != 100 || !*b.Muted {
		t.Errorf("second = %+v", b)
	}
}

func TestMatchAudioApps(t *testing.T) {
	apps := []audioApp{
		{ID: "1", Name: "chrome", PID: 10},
		{ID: "2", Name: "chrome", PID: 11},
		{ID: "3", Name: "Spotify", PID: 12},
		{ID: "4", Name: "Microsoft Teams", PID: 13},
		{ID: "5", Name: "Microsoft Edge", PID: 14},
	}
	if got, err := matchAudioApps(apps, "Chrome"); err != nil || len(got) != 2 {
		t.Errorf("every chrome stream should match: %+v %v", got, err)
	}
	if got, err := matchAudioApps(apps, "spot"); err != nil || len(got) != 1 || got[0].ID != "3" {
		t.Errorf("substring: %+v %v", got, err)
	}
	if got, err := matchAudioApps(apps, "13"); err != nil || len(got) != 1 || got[0].Name != "Microsoft Teams" {
		t.Errorf("pid: %+v %v", got, err)
	}
	if _, err := matchAudioApps(apps, "microsoft"); err == nil || !strings.Contains(err.Error(), "matches 2 apps") {
		t.Errorf("ambiguous should be refused: %v", err)
	}
	if _, err := matchAudioApps(apps, "zoom"); err == nil {
		t.Error("no match should fail")
	}
}
