package groups

import (
	"fmt"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine"
	"golang.org/x/sys/windows"
)

var (
	modeUser32               = windows.NewLazySystemDLL("user32.dll")
	procEnumDisplayDevices   = modeUser32.NewProc("EnumDisplayDevicesW")
	procEnumDisplaySettings  = modeUser32.NewProc("EnumDisplaySettingsExW")
	procChangeDisplaySetting = modeUser32.NewProc("ChangeDisplaySettingsExW")
	procSetDisplayConfig     = modeUser32.NewProc("SetDisplayConfig")
)

// displayDevice is DISPLAY_DEVICEW.
type displayDevice struct {
	Cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

// devMode is DEVMODEW, display variant.
type devMode struct {
	DeviceName       [32]uint16
	SpecVersion      uint16
	DriverVersion    uint16
	Size             uint16
	DriverExtra      uint16
	Fields           uint32
	PositionX        int32
	PositionY        int32
	DisplayOrient    uint32
	DisplayFixed     uint32
	Color            int16
	Duplex           int16
	YResolution      int16
	TTOption         int16
	Collate          int16
	FormName         [32]uint16
	LogPixels        uint16
	BitsPerPel       uint32
	PelsWidth        uint32
	PelsHeight       uint32
	DisplayFlags     uint32
	DisplayFrequency uint32
	ICMMethod        uint32
	ICMIntent        uint32
	MediaType        uint32
	DitherType       uint32
	Reserved1        uint32
	Reserved2        uint32
	PanningWidth     uint32
	PanningHeight    uint32
}

const (
	enumCurrentSettings  = ^uintptr(0) // ENUM_CURRENT_SETTINGS (-1)
	dmPelsWidth          = 0x80000
	dmPelsHeight         = 0x100000
	dmDisplayFrequency   = 0x400000
	cdsUpdateRegistry    = 0x1
	cdsTest              = 0x2
	dispChangeSuccessful = 0
	attachedToDesktop    = 0x1
	sdcTopologyClone     = 0x2
	sdcTopologyExtend    = 0x4
	sdcApply             = 0x80
)

// deviceFor finds the \\.\DISPLAYn whose desktop position matches mon.
func deviceFor(mon engine.Monitor) ([32]uint16, error) {
	for i := uintptr(0); i < 32; i++ {
		var dd displayDevice
		dd.Cb = uint32(unsafe.Sizeof(dd))
		if r, _, _ := procEnumDisplayDevices.Call(0, i, uintptr(unsafe.Pointer(&dd)), 0); r == 0 {
			break
		}
		if dd.StateFlags&attachedToDesktop == 0 {
			continue
		}
		var dm devMode
		dm.Size = uint16(unsafe.Sizeof(dm))
		if r, _, _ := procEnumDisplaySettings.Call(uintptr(unsafe.Pointer(&dd.DeviceName[0])), enumCurrentSettings, uintptr(unsafe.Pointer(&dm)), 0); r == 0 {
			continue
		}
		if int(dm.PositionX) == mon.X && int(dm.PositionY) == mon.Y {
			return dd.DeviceName, nil
		}
	}
	return [32]uint16{}, fmt.Errorf("no display device at %d,%d", mon.X, mon.Y)
}

func listDisplayModes(mon engine.Monitor) ([]displayMode, error) {
	dev, err := deviceFor(mon)
	if err != nil {
		return nil, err
	}
	var cur devMode
	cur.Size = uint16(unsafe.Sizeof(cur))
	procEnumDisplaySettings.Call(uintptr(unsafe.Pointer(&dev[0])), enumCurrentSettings, uintptr(unsafe.Pointer(&cur)), 0)
	var out []displayMode
	for i := uintptr(0); ; i++ {
		var dm devMode
		dm.Size = uint16(unsafe.Sizeof(dm))
		if r, _, _ := procEnumDisplaySettings.Call(uintptr(unsafe.Pointer(&dev[0])), i, uintptr(unsafe.Pointer(&dm)), 0); r == 0 {
			break
		}
		if dm.BitsPerPel < 32 {
			continue
		}
		out = append(out, displayMode{
			Width: int(dm.PelsWidth), Height: int(dm.PelsHeight), Refresh: float64(dm.DisplayFrequency),
			Current: dm.PelsWidth == cur.PelsWidth && dm.PelsHeight == cur.PelsHeight && dm.DisplayFrequency == cur.DisplayFrequency,
		})
	}
	return out, nil
}

// setDisplayMode tests the mode first (CDS_TEST) so a mode the driver would
// reject never blanks the screen, then applies it.
func setDisplayMode(mon engine.Monitor, m displayMode) error {
	dev, err := deviceFor(mon)
	if err != nil {
		return err
	}
	var dm devMode
	dm.Size = uint16(unsafe.Sizeof(dm))
	dm.Fields = dmPelsWidth | dmPelsHeight | dmDisplayFrequency
	dm.PelsWidth, dm.PelsHeight, dm.DisplayFrequency = uint32(m.Width), uint32(m.Height), uint32(m.Refresh+0.5)
	for _, flags := range []uintptr{cdsTest, cdsUpdateRegistry} {
		if r, _, _ := procChangeDisplaySetting.Call(uintptr(unsafe.Pointer(&dev[0])), uintptr(unsafe.Pointer(&dm)), 0, flags, 0); int32(r) != dispChangeSuccessful {
			return fmt.Errorf("Windows refused the mode (DISP_CHANGE %d)", int32(r))
		}
	}
	return nil
}

func setMirror(on bool) error {
	topo := uintptr(sdcTopologyExtend)
	if on {
		topo = sdcTopologyClone
	}
	if r, _, _ := procSetDisplayConfig.Call(0, 0, 0, 0, topo|sdcApply); r != 0 {
		return windows.Errno(r)
	}
	return nil
}
