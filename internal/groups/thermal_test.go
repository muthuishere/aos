package groups

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseIORegBattery(t *testing.T) {
	h := parseIORegBattery(`      "BatteryData" = {"CycleCount"=99,"DesignCapacity"=1}
      "NominalChargeCapacity" = 8569
      "AppleRawMaxCapacity" = 8325
      "DesignCapacity" = 8579
      "CycleCount" = 15
      "Temperature" = 3067
`)
	if h.CycleCount == nil || *h.CycleCount != 15 {
		t.Errorf("cycles = %v (the nested BatteryData copy must not win)", h.CycleCount)
	}
	if h.HealthPercent == nil || *h.HealthPercent != 100 {
		t.Errorf("health = %v, want 100 (8569/8579 rounds up)", h.HealthPercent)
	}
	if h.TemperatureC == nil || *h.TemperatureC != 30.67 {
		t.Errorf("temp = %v", h.TemperatureC)
	}
	if got := describeHealth(h); got != "health 100%, 15 cycles, 30.7°C" {
		t.Errorf("describe = %q", got)
	}
	// Older Intel Macs have no NominalChargeCapacity.
	h = parseIORegBattery("\"AppleRawMaxCapacity\" = 4000\n\"DesignCapacity\" = 5000\n")
	if h.HealthPercent == nil || *h.HealthPercent != 80 {
		t.Errorf("raw fallback health = %v", h.HealthPercent)
	}
}

func TestSysfsBatteryHealth(t *testing.T) {
	dir := t.TempDir()
	write := func(name, v string) { os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o644) }
	write("cycle_count", "312")
	write("charge_full", "4100000")
	write("charge_full_design", "5000000")
	write("temp", "315")
	h := sysfsBatteryHealth(dir)
	if *h.CycleCount != 312 || *h.HealthPercent != 82 || *h.TemperatureC != 31.5 {
		t.Errorf("got %d %d %v", *h.CycleCount, *h.HealthPercent, *h.TemperatureC)
	}
	if describeHealth(sysfsBatteryHealth(t.TempDir())) != "" {
		t.Error("an empty battery dir should describe as nothing")
	}
}

func TestHealthPercent(t *testing.T) {
	if p := healthPercent(5200, 5000); *p != 100 {
		t.Errorf("above design should cap at 100, got %d", *p)
	}
	if healthPercent(0, 5000) != nil || healthPercent(5000, 0) != nil {
		t.Error("unknown capacity should be nil")
	}
}

func TestReadThermalZones(t *testing.T) {
	root := t.TempDir()
	for name, v := range map[string][2]string{
		"thermal_zone0": {"x86_pkg_temp", "54000"},
		"thermal_zone1": {"acpitz", "0"}, // an unpopulated zone
		"thermal_zone2": {"", "41500"},
	} {
		d := filepath.Join(root, name)
		os.MkdirAll(d, 0o755)
		if v[0] != "" {
			os.WriteFile(filepath.Join(d, "type"), []byte(v[0]), 0o644)
		}
		os.WriteFile(filepath.Join(d, "temp"), []byte(v[1]), 0o644)
	}
	got := readThermalZones(root)
	if len(got) != 2 || got[0] != (thermalZone{"thermal_zone2", 41.5}) || got[1] != (thermalZone{"x86_pkg_temp", 54}) {
		t.Errorf("got %+v", got)
	}
}

func TestThermalStateName(t *testing.T) {
	for v, want := range map[int]string{0: "nominal", 1: "fair", 2: "serious", 3: "critical", 7: ""} {
		if got := thermalStateName(v); got != want {
			t.Errorf("%d -> %q, want %q", v, got, want)
		}
	}
}
