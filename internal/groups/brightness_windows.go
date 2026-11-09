package groups

import (
	"fmt"
	"unsafe"

	"github.com/muthuishere/aos/internal/engine"
	"golang.org/x/sys/windows"
)

// Laptop panels answer the video brightness IOCTLs on \\.\LCD; external
// monitors answer DDC/CI through dxva2. Neither needs WMI or a shell.
const (
	ioctlVideoQueryDisplayBrightness = 0x00230498
	ioctlVideoSetDisplayBrightness   = 0x0023049C
	displayPolicyBoth                = 3
	monitorDefaultToNull             = 0

	qdcOnlyActivePaths        = 2
	dcGetSourceName           = 1
	dcGetTargetName           = 2
	outputTechInternal        = 0x80000000
	outputTechDisplayPortEmb  = 11
	outputTechUDIEmbedded     = 13
	errorInsufficientBuffer   = 122
	displayConfigPathInfoSize = 72
	displayConfigModeInfoSize = 64
)

type displayBrightness struct{ Policy, AC, DC byte }

type physicalMonitor struct {
	Handle      windows.Handle
	Description [128]uint16
}

type monitorInfoEx struct {
	Size    uint32
	Monitor [4]int32
	Work    [4]int32
	Flags   uint32
	Device  [32]uint16
}

type dcHeader struct {
	Type    uint32
	Size    uint32
	Adapter windows.LUID
	ID      uint32
}

type dcSourceName struct {
	Header  dcHeader
	GDIName [32]uint16
}

type dcTargetName struct {
	Header           dcHeader
	Flags            uint32
	OutputTechnology uint32
	EdidManufacture  uint16
	EdidProduct      uint16
	ConnectorInst    uint32
	FriendlyName     [64]uint16
	DevicePath       [128]uint16
}

var (
	dxva2                       = windows.NewLazySystemDLL("dxva2.dll")
	procGetNumPhysicalMonitors  = dxva2.NewProc("GetNumberOfPhysicalMonitorsFromHMONITOR")
	procGetPhysicalMonitors     = dxva2.NewProc("GetPhysicalMonitorsFromHMONITOR")
	procGetMonitorBrightness    = dxva2.NewProc("GetMonitorBrightness")
	procSetMonitorBrightness    = dxva2.NewProc("SetMonitorBrightness")
	procDestroyPhysicalMonitors = dxva2.NewProc("DestroyPhysicalMonitors")
	user32b                     = windows.NewLazySystemDLL("user32.dll")
	procMonitorFromPoint        = user32b.NewProc("MonitorFromPoint")
	procGetMonitorInfoW         = user32b.NewProc("GetMonitorInfoW")
	procGetDisplayConfigSizes   = user32b.NewProc("GetDisplayConfigBufferSizes")
	procQueryDisplayConfig      = user32b.NewProc("QueryDisplayConfig")
	procDisplayConfigGetInfo    = user32b.NewProc("DisplayConfigGetDeviceInfo")
)

type winTarget struct {
	Name     string
	Internal bool
}

// displayTargets maps each GDI device (\\.\DISPLAY1) to its monitor's
// friendly name and whether it is the built-in panel.
func displayTargets() map[string]winTarget {
	res := map[string]winTarget{}
	var nPaths, nModes uint32
	for try := 0; try < 3; try++ {
		if r, _, _ := procGetDisplayConfigSizes.Call(qdcOnlyActivePaths,
			uintptr(unsafe.Pointer(&nPaths)), uintptr(unsafe.Pointer(&nModes))); r != 0 || nPaths == 0 {
			return res
		}
		paths := make([]byte, int(nPaths)*displayConfigPathInfoSize)
		modes := make([]byte, int(nModes)*displayConfigModeInfoSize+1)
		r, _, _ := procQueryDisplayConfig.Call(qdcOnlyActivePaths,
			uintptr(unsafe.Pointer(&nPaths)), uintptr(unsafe.Pointer(&paths[0])),
			uintptr(unsafe.Pointer(&nModes)), uintptr(unsafe.Pointer(&modes[0])), 0)
		if r == errorInsufficientBuffer {
			continue
		}
		if r != 0 {
			return res
		}
		for i := 0; i < int(nPaths); i++ {
			p := paths[i*displayConfigPathInfoSize:]
			// sourceInfo: LUID(8) id(4) ...; targetInfo starts at 20: LUID(8) id(4).
			src := dcSourceName{Header: dcHeader{Type: dcGetSourceName, Size: uint32(unsafe.Sizeof(dcSourceName{}))}}
			src.Header.Adapter = *(*windows.LUID)(unsafe.Pointer(&p[0]))
			src.Header.ID = *(*uint32)(unsafe.Pointer(&p[8]))
			tgt := dcTargetName{Header: dcHeader{Type: dcGetTargetName, Size: uint32(unsafe.Sizeof(dcTargetName{}))}}
			tgt.Header.Adapter = *(*windows.LUID)(unsafe.Pointer(&p[20]))
			tgt.Header.ID = *(*uint32)(unsafe.Pointer(&p[28]))
			if r, _, _ := procDisplayConfigGetInfo.Call(uintptr(unsafe.Pointer(&src))); r != 0 {
				continue
			}
			if r, _, _ := procDisplayConfigGetInfo.Call(uintptr(unsafe.Pointer(&tgt))); r != 0 {
				continue
			}
			t := tgt.OutputTechnology
			res[windows.UTF16ToString(src.GDIName[:])] = winTarget{
				Name:     windows.UTF16ToString(tgt.FriendlyName[:]),
				Internal: t == outputTechInternal || t == outputTechDisplayPortEmb || t == outputTechUDIEmbedded,
			}
		}
		return res
	}
	return res
}

func hmonitorOf(m engine.Monitor) uintptr {
	x, y := int32(m.X+m.Width/2), int32(m.Y+m.Height/2)
	pt := uintptr(uint32(x)) | uintptr(uint32(y))<<32
	h, _, _ := procMonitorFromPoint.Call(pt, monitorDefaultToNull)
	return h
}

func gdiDevice(hmon uintptr) string {
	mi := monitorInfoEx{Size: uint32(unsafe.Sizeof(monitorInfoEx{}))}
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return ""
	}
	return windows.UTF16ToString(mi.Device[:])
}

// monitorName is the friendly name Windows shows in Display settings.
func monitorName(m engine.Monitor) string {
	if t, ok := displayTargets()[gdiDevice(hmonitorOf(m))]; ok && t.Name != "" {
		return t.Name
	}
	return fmt.Sprintf("Display %d", m.ID)
}

func brightnessScreens(ms []engine.Monitor) ([]screen, error) {
	targets := displayTargets()
	out := make([]screen, 0, len(ms))
	for _, m := range ms {
		s := screen{ID: m.ID, Name: fmt.Sprintf("Display %d", m.ID)}
		hmon := hmonitorOf(m)
		t, known := targets[gdiDevice(hmon)]
		if known && t.Name != "" {
			s.Name = t.Name
		} else if known && t.Internal {
			s.Name = "Built-in Display"
		}
		if known && t.Internal && lcdAvailable() {
			s.Method = "builtin"
			s.get, s.set = getLCD, setLCD
		} else {
			ddcScreenWin(&s, hmon)
		}
		out = append(out, s)
	}
	return out, nil
}

func openLCD() (windows.Handle, error) {
	p, _ := windows.UTF16PtrFromString(`\\.\LCD`)
	return windows.CreateFile(p, windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
}

func lcdAvailable() bool {
	_, err := getLCD()
	return err == nil
}

func getLCD() (int, error) {
	h, err := openLCD()
	if err != nil {
		return 0, fmt.Errorf("the panel exposes no brightness control: %v", err)
	}
	defer windows.CloseHandle(h)
	var b displayBrightness
	var n uint32
	if err := windows.DeviceIoControl(h, ioctlVideoQueryDisplayBrightness, nil, 0,
		(*byte)(unsafe.Pointer(&b)), uint32(unsafe.Sizeof(b)), &n, nil); err != nil {
		return 0, fmt.Errorf("could not read the panel's brightness: %v", err)
	}
	return int(b.AC), nil
}

func setLCD(pct int) error {
	h, err := openLCD()
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	b := displayBrightness{Policy: displayPolicyBoth, AC: byte(pct), DC: byte(pct)}
	var n uint32
	return windows.DeviceIoControl(h, ioctlVideoSetDisplayBrightness,
		(*byte)(unsafe.Pointer(&b)), uint32(unsafe.Sizeof(b)), nil, 0, &n, nil)
}

// withPhysical opens the monitor's first physical handle for one call.
func withPhysical(hmon uintptr, f func(h uintptr) error) error {
	var n uint32
	if r, _, _ := procGetNumPhysicalMonitors.Call(hmon, uintptr(unsafe.Pointer(&n))); r == 0 || n == 0 {
		return fmt.Errorf("Windows exposes no physical monitor handle for it")
	}
	pms := make([]physicalMonitor, n)
	if r, _, _ := procGetPhysicalMonitors.Call(hmon, uintptr(n), uintptr(unsafe.Pointer(&pms[0]))); r == 0 {
		return fmt.Errorf("Windows exposes no physical monitor handle for it")
	}
	defer procDestroyPhysicalMonitors.Call(uintptr(n), uintptr(unsafe.Pointer(&pms[0])))
	return f(uintptr(pms[0].Handle))
}

func ddcRange(h uintptr) (min, cur, max uint32, err error) {
	r, _, _ := procGetMonitorBrightness.Call(h,
		uintptr(unsafe.Pointer(&min)), uintptr(unsafe.Pointer(&cur)), uintptr(unsafe.Pointer(&max)))
	if r == 0 || max <= min {
		return 0, 0, 0, fmt.Errorf("the monitor does not answer DDC/CI (it may be off in the monitor's menu)")
	}
	return min, cur, max, nil
}

func ddcScreenWin(s *screen, hmon uintptr) {
	s.Method = "ddc"
	if hmon == 0 {
		s.Reason = "Windows has no monitor handle for it"
		return
	}
	s.get = func() (int, error) {
		var pct int
		err := withPhysical(hmon, func(h uintptr) error {
			min, cur, max, err := ddcRange(h)
			pct = toPercent(int(cur-min), int(max-min))
			return err
		})
		return pct, err
	}
	s.set = func(pct int) error {
		return withPhysical(hmon, func(h uintptr) error {
			min, _, max, err := ddcRange(h)
			if err != nil {
				return err
			}
			level := uint32(fromPercent(pct, int(max-min))) + min
			if r, _, _ := procSetMonitorBrightness.Call(h, uintptr(level)); r == 0 {
				return fmt.Errorf("the monitor refused the DDC/CI write")
			}
			return nil
		})
	}
}
