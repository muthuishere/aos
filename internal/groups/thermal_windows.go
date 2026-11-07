package groups

// Windows exposes temperatures only through WMI (ACPI thermal zones, and
// usually only to administrators) and battery wear only through the battery
// device IOCTLs; neither is wired up yet, so `system thermal` is declared for
// macOS and Linux and battery status shows no health line here.
func readThermal() (thermalInfo, error) { return thermalInfo{}, errUnsupported }

func readBatteryHealth() (batteryHealth, error) { return batteryHealth{}, nil }
