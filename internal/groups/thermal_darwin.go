package groups

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
static int aosThermalState(void) { return (int)[[NSProcessInfo processInfo] thermalState]; }
*/
import "C"

import "github.com/muthuishere/aos/internal/sys"

// readThermal reports the OS throttling state. macOS has no public API for
// die temperatures (that is the SMC, behind private calls), so the only
// sensor shown is the battery's own.
func readThermal() (thermalInfo, error) {
	info := thermalInfo{State: thermalStateName(int(C.aosThermalState()))}
	if h, err := readBatteryHealth(); err == nil && h.TemperatureC != nil {
		info.Sensors = append(info.Sensors, thermalZone{Name: "battery", TemperatureC: *h.TemperatureC})
	}
	return info, nil
}

func readBatteryHealth() (batteryHealth, error) {
	out, err := sys.Output("ioreg", "-rn", "AppleSmartBattery")
	if err != nil || out == "" {
		return batteryHealth{}, err
	}
	return parseIORegBattery(out), nil
}
