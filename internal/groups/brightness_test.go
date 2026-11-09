package groups

import (
	"testing"

	"github.com/muthuishere/aos/internal/engine"
)

func TestBrightnessTarget(t *testing.T) {
	cases := []struct {
		arg  string
		cur  int
		want int
	}{{"50", 10, 50}, {"+10", 95, 100}, {"-10", 5, 0}, {"-10", 40, 30}, {"150", 0, 100}}
	for _, tc := range cases {
		got, err := brightnessTarget(tc.arg, tc.cur)
		if err != nil || got != tc.want {
			t.Errorf("brightnessTarget(%q, %d) = %d, %v; want %d", tc.arg, tc.cur, got, err, tc.want)
		}
	}
	if _, err := brightnessTarget("bright", 0); err == nil {
		t.Error("non-number should fail")
	}
}

func TestBrightnessScale(t *testing.T) {
	if toPercent(480, 960) != 50 || toPercent(1, 0) != 0 || fromPercent(50, 960) != 480 || fromPercent(100, 255) != 255 {
		t.Error("scale conversion wrong")
	}
}

func TestParseBrightnessctl(t *testing.T) {
	got, err := parseBrightnessctl("intel_backlight,backlight,400,42%,960\n")
	if err != nil || got != 42 {
		t.Errorf("got %d, %v", got, err)
	}
	if _, err := parseBrightnessctl("garbage"); err == nil {
		t.Error("garbage should fail")
	}
}

func TestDDCReply(t *testing.T) {
	reply := []byte{0x6E, 0x88, 0x02, 0x00, 0x10, 0x00, 0x00, 0x64, 0x00, 0x4C, 0x00, 0x00}
	cur, max, err := ddcReply(reply, 0x10)
	if err != nil || cur != 76 || max != 100 {
		t.Errorf("got %d/%d, %v", cur, max, err)
	}
	if _, _, err := ddcReply([]byte{0x6E, 0x88, 0x02, 0x01, 0x10, 0, 0, 0, 0, 0, 0}, 0x10); err == nil {
		t.Error("unsupported feature should fail")
	}
	if _, _, err := ddcReply(make([]byte, 12), 0x10); err == nil {
		t.Error("empty reply should fail")
	}
}

func TestDdcutil(t *testing.T) {
	ds := parseDdcutilDetect("Display 1\n   I2C bus:  /dev/i2c-4\n   DRM connector:    card0-HDMI-A-1\n   Monitor:          BNQ:BenQ GW2780:J7M\n\nDisplay 2\n   DRM connector: card1-DP-2\n   Monitor: DEL:DELL U2415:X\n")
	if len(ds) != 2 || ds[0].Model != "BenQ GW2780" || ds[1].Connector != "card1-DP-2" {
		t.Fatalf("parse: %+v", ds)
	}
	if d, ok := matchDdcutil(ds, "HDMI-1"); !ok || d.Number != 1 {
		t.Errorf("HDMI-1 -> %+v %v", d, ok)
	}
	if d, ok := matchDdcutil(ds, "DP-2"); !ok || d.Number != 2 {
		t.Errorf("DP-2 -> %+v %v", d, ok)
	}
	if _, ok := matchDdcutil(ds, "eDP-1"); ok {
		t.Error("eDP-1 must not match")
	}
	if v, err := parseDdcutilVCP("VCP 10 C 32 64\n"); err != nil || v != 50 {
		t.Errorf("vcp %d %v", v, err)
	}
	if _, err := parseDdcutilVCP("garbage"); err == nil {
		t.Error("garbage should fail")
	}
}

func TestDefaultMonitorID(t *testing.T) {
	ms := []engine.Monitor{{ID: 1, Primary: true}, {ID: 2, Active: true}, {ID: 3, Focused: true}}
	if got := defaultMonitorID(ms); got != 3 {
		t.Errorf("focused should win, got %d", got)
	}
	ms[2].Focused = false
	if got := defaultMonitorID(ms); got != 2 {
		t.Errorf("pointer should be next, got %d", got)
	}
}

func TestBrightnessArgs(t *testing.T) {
	for _, tc := range []struct {
		args  []string
		id    int
		level string
		bad   bool
	}{
		{nil, 0, "", false},
		{[]string{"+20"}, 0, "+20", false},
		{[]string{"2", "50"}, 2, "50", false},
		{[]string{"--monitor=3"}, 3, "", false},
		{[]string{"--monitor=3", "-10"}, 3, "-10", false},
		{[]string{"x", "50"}, 0, "", true},
		{[]string{"bright"}, 0, "", true},
		{[]string{"--monitor=3", "2", "50"}, 0, "", true},
	} {
		set, _ := parseArgs(tc.args, "monitor")
		id, level, err := brightnessArgs(set)
		if (err != nil) != tc.bad || (!tc.bad && (id != tc.id || level != tc.level)) {
			t.Errorf("%v -> %d %q %v", tc.args, id, level, err)
		}
	}
}
