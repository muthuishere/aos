package groups

import (
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemPowerStatus is SYSTEM_POWER_STATUS.
type systemPowerStatus struct {
	ACLineStatus        byte // 0 offline, 1 online, 255 unknown
	BatteryFlag         byte // 8 charging, 128 no battery, 255 unknown
	BatteryLifePercent  byte // 255 unknown
	SystemStatusFlag    byte
	BatteryLifeTime     uint32 // seconds, 0xFFFFFFFF unknown
	BatteryFullLifeTime uint32
}

var procGetSystemPowerStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemPowerStatus")

func readPower() (powerState, error) {
	state := powerState{Percent: -1}
	var s systemPowerStatus
	if r, _, err := procGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&s))); r == 0 {
		return state, err
	}
	state.HasBattery = s.BatteryFlag != 128 && s.BatteryFlag != 255
	// No battery at all means a desktop, which is always on mains.
	state.OnAC = s.ACLineStatus == 1 || (!state.HasBattery && s.ACLineStatus != 0)
	if !state.HasBattery {
		return state, nil
	}
	if s.BatteryLifePercent <= 100 {
		state.Percent = int(s.BatteryLifePercent)
	}
	switch {
	case s.BatteryFlag&8 != 0:
		state.Status = "charging"
	case state.OnAC && state.Percent == 100:
		state.Status = "charged"
	case state.OnAC:
		state.Status = "not charging"
	default:
		state.Status = "discharging"
	}
	if s.BatteryLifeTime != 0xFFFFFFFF && !state.OnAC {
		minutes := int(s.BatteryLifeTime / 60)
		state.Remaining = strconv.Itoa(minutes/60) + ":" + leftPad(minutes%60)
	}
	return state, nil
}

func leftPad(n int) string {
	s := strconv.Itoa(n)
	if len(s) < 2 {
		return strings.Repeat("0", 2-len(s)) + s
	}
	return s
}
