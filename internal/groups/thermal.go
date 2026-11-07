package groups

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// batteryHealth is the wear side of the battery. Nil fields are ones the
// platform does not report.
type batteryHealth struct {
	CycleCount    *int     `json:"cycle_count,omitempty"`
	HealthPercent *int     `json:"health_percent,omitempty"` // full-charge capacity / design capacity
	TemperatureC  *float64 `json:"temperature_c,omitempty"`
}

// thermalInfo is what `system thermal --json` emits.
type thermalInfo struct {
	// State is nominal, fair, serious or critical: how hard the OS is
	// throttling to stay cool. "" where the platform has no such rating.
	State   string        `json:"state,omitempty"`
	Sensors []thermalZone `json:"sensors,omitempty"`
}

type thermalZone struct {
	Name         string  `json:"name"`
	TemperatureC float64 `json:"temperature_c"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "system", Name: "thermal",
			Summary: "Print the thermal state (throttling) and temperatures the OS exposes",
			Args:    "[--json]",
			Examples: []string{
				"aos system thermal",
				"aos system thermal --json",
			},
			Platforms: []string{"darwin", "linux"},
			Run:       runSystemThermal,
		})
	})
}

func runSystemThermal(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("system thermal", args)
	if err != nil {
		return err
	}
	info, err := readThermal()
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(c, info)
	}
	if info.State != "" {
		c.Printf("state  %s\n", info.State)
	}
	for _, z := range info.Sensors {
		c.Printf("%-24s %.1f°C\n", z.Name, z.TemperatureC)
	}
	if info.State == "" && len(info.Sensors) == 0 {
		c.Println("no thermal readings available")
	}
	return nil
}

// thermalStateName maps NSProcessInfoThermalState (0-3).
func thermalStateName(v int) string {
	switch v {
	case 0:
		return "nominal"
	case 1:
		return "fair"
	case 2:
		return "serious"
	case 3:
		return "critical"
	}
	return ""
}

// readThermalZones reads Linux /sys/class/thermal/thermal_zone*/{type,temp}
// (millidegrees Celsius) under root.
func readThermalZones(root string) []thermalZone {
	dirs, _ := filepath.Glob(filepath.Join(root, "thermal_zone*"))
	var out []thermalZone
	for _, d := range dirs {
		milli, err := strconv.Atoi(readSysfs(d, "temp"))
		if err != nil || milli <= 0 {
			continue
		}
		name := readSysfs(d, "type")
		if name == "" {
			name = filepath.Base(d)
		}
		out = append(out, thermalZone{Name: name, TemperatureC: float64(milli) / 1000})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func readSysfs(dir, name string) string {
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

var ioregInt = regexp.MustCompile(`(?m)^\s*"(CycleCount|DesignCapacity|NominalChargeCapacity|AppleRawMaxCapacity|Temperature)" = (\d+)\s*$`)

// parseIORegBattery reads `ioreg -rn AppleSmartBattery`. Health uses the
// nominal full-charge capacity, which is what System Settings shows as
// Maximum Capacity; temperature is in hundredths of a degree.
func parseIORegBattery(out string) batteryHealth {
	v := map[string]int{}
	for _, m := range ioregInt.FindAllStringSubmatch(out, -1) {
		if n, err := strconv.Atoi(m[2]); err == nil {
			v[m[1]] = n
		}
	}
	var h batteryHealth
	if n, ok := v["CycleCount"]; ok {
		h.CycleCount = &n
	}
	full := v["NominalChargeCapacity"]
	if full == 0 {
		full = v["AppleRawMaxCapacity"]
	}
	h.HealthPercent = healthPercent(full, v["DesignCapacity"])
	if t, ok := v["Temperature"]; ok && t > 0 {
		c := float64(t) / 100
		h.TemperatureC = &c
	}
	return h
}

// sysfsBatteryHealth reads one Linux power_supply battery directory: energy_*
// (µWh) or charge_* (µAh), whichever the driver provides.
func sysfsBatteryHealth(dir string) batteryHealth {
	var h batteryHealth
	if n, err := strconv.Atoi(readSysfs(dir, "cycle_count")); err == nil && n > 0 {
		h.CycleCount = &n
	}
	for _, kind := range []string{"energy", "charge"} {
		full, err1 := strconv.Atoi(readSysfs(dir, kind+"_full"))
		design, err2 := strconv.Atoi(readSysfs(dir, kind+"_full_design"))
		if err1 == nil && err2 == nil {
			h.HealthPercent = healthPercent(full, design)
			break
		}
	}
	if t, err := strconv.Atoi(readSysfs(dir, "temp")); err == nil && t > 0 {
		c := float64(t) / 10 // tenths of a degree
		h.TemperatureC = &c
	}
	return h
}

// healthPercent is full/design, capped at 100 (a new pack can read above
// design), or nil when either is unknown.
func healthPercent(full, design int) *int {
	if full <= 0 || design <= 0 {
		return nil
	}
	p := (full*100 + design/2) / design
	if p > 100 {
		p = 100
	}
	return &p
}

// describeHealth is the battery status suffix: "health 97%, 15 cycles, 30.7°C".
func describeHealth(h batteryHealth) string {
	var parts []string
	if h.HealthPercent != nil {
		parts = append(parts, fmt.Sprintf("health %d%%", *h.HealthPercent))
	}
	if h.CycleCount != nil {
		parts = append(parts, fmt.Sprintf("%d cycles", *h.CycleCount))
	}
	if h.TemperatureC != nil {
		parts = append(parts, fmt.Sprintf("%.1f°C", *h.TemperatureC))
	}
	return strings.Join(parts, ", ")
}
