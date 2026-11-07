package groups

import "testing"

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
