package groups

import "path/filepath"

// readThermal lists the kernel's thermal zones. Linux has no single
// throttling rating, so State stays empty.
func readThermal() (thermalInfo, error) {
	return thermalInfo{Sensors: readThermalZones("/sys/class/thermal")}, nil
}

func readBatteryHealth() (batteryHealth, error) {
	dirs, _ := filepath.Glob(filepath.Join(powerSupplyDir, "*"))
	for _, d := range dirs {
		if readSysfs(d, "type") == "Battery" {
			return sysfsBatteryHealth(d), nil
		}
	}
	return batteryHealth{}, nil
}
