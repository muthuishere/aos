package groups

import (
	"strings"
	"testing"
)

func TestParseModeSpec(t *testing.T) {
	for in, want := range map[string][3]float64{
		"2560x1440":       {2560, 1440, 0},
		"1920x1080@60":    {1920, 1080, 60},
		"1920X1080@59.94": {1920, 1080, 59.94},
		"3840x2160@120hz": {3840, 2160, 120},
	} {
		w, h, hz, err := parseModeSpec(in)
		if err != nil || float64(w) != want[0] || float64(h) != want[1] || hz != want[2] {
			t.Errorf("parseModeSpec(%q) = %d %d %v %v", in, w, h, hz, err)
		}
	}
	for _, bad := range []string{"1920", "axb", "1920x1080@", "0x10", "1920x1080@fast"} {
		if _, _, _, err := parseModeSpec(bad); err == nil {
			t.Errorf("parseModeSpec(%q) should fail", bad)
		}
	}
}

func TestPickMode(t *testing.T) {
	modes := []displayMode{
		{Width: 1920, Height: 1080, Refresh: 60},
		{Width: 1920, Height: 1080, Refresh: 144},
		{Width: 1920, Height: 1080, Refresh: 59.94},
		{Width: 2560, Height: 1440, Refresh: 60, Current: true},
	}
	if m, err := pickMode(modes, 1920, 1080, 0); err != nil || m.Refresh != 144 {
		t.Errorf("no hz should pick the highest: %+v %v", m, err)
	}
	if m, err := pickMode(modes, 1920, 1080, 60); err != nil || m.Refresh != 60 {
		t.Errorf("@60 = %+v %v", m, err)
	}
	if _, err := pickMode(modes, 1920, 1080, 75); err == nil {
		t.Error("@75 is not offered")
	}
	_, err := pickMode(modes, 1280, 720, 0)
	if err == nil || !strings.Contains(err.Error(), "2560x1440@60") || strings.Contains(err.Error(), "current") {
		t.Errorf("unknown mode should list the choices: %v", err)
	}
}

func TestSortModes(t *testing.T) {
	got := sortModes([]displayMode{
		{Width: 1920, Height: 1080, Refresh: 60},
		{Width: 2560, Height: 1440, Refresh: 60},
		{Width: 1920, Height: 1080, Refresh: 60, Current: true},
		{Width: 1920, Height: 1080, Refresh: 120},
	})
	if len(got) != 3 || got[0].Width != 2560 || got[1].Refresh != 120 || !got[2].Current {
		t.Errorf("got %+v", got)
	}
}

func TestParseXrandr(t *testing.T) {
	outs := parseXrandr(`Screen 0: minimum 320 x 200, current 4480 x 1440, maximum 16384 x 16384
eDP-1 connected primary 1920x1080+2560+0 (normal left inverted right x axis y axis) 344mm x 194mm
   1920x1080     60.02*+  59.97    48.00
   1280x720      60.00
HDMI-1 connected 2560x1440+0+0 (normal left inverted right x axis y axis) 597mm x 336mm
   2560x1440     59.95*+
   1920x1080i    60.00
DP-1 disconnected (normal left inverted right x axis y axis)
`)
	if len(outs) != 2 || outs[0].Name != "eDP-1" || outs[0].X != 2560 || outs[1].X != 0 {
		t.Fatalf("outputs = %+v", outs)
	}
	m := outs[0].Modes
	if len(m) != 4 || !m[0].Current || m[0].Refresh != 60.02 || m[3].Width != 1280 {
		t.Errorf("eDP-1 modes = %+v", m)
	}
	if outs[1].Modes[1].Width != 1920 {
		t.Errorf("interlaced mode not parsed: %+v", outs[1].Modes)
	}
}
