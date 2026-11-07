package groups

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// processorPerformance is SYSTEM_PROCESSOR_PERFORMANCE_INFORMATION. Kernel
// time includes idle time, so busy is kernel+user-idle.
type processorPerformance struct {
	IdleTime       int64
	KernelTime     int64
	UserTime       int64
	DpcTime        int64
	InterruptTime  int64
	InterruptCount uint32
	_              uint32
}

var procGetTickCount64 = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetTickCount64")

const systemProcessorPerformanceInformation = 8

func readCPUTicks() ([]cpuTicks, error) {
	buf := make([]processorPerformance, 1024)
	var got uint32
	size := uint32(len(buf)) * uint32(unsafe.Sizeof(buf[0]))
	if err := windows.NtQuerySystemInformation(systemProcessorPerformanceInformation, unsafe.Pointer(&buf[0]), size, &got); err != nil {
		return nil, err
	}
	n := int(got / uint32(unsafe.Sizeof(buf[0])))
	out := make([]cpuTicks, n)
	for i, p := range buf[:n] {
		out[i] = windowsTicks(p.IdleTime, p.KernelTime, p.UserTime)
	}
	return out, nil
}

// Windows keeps no load average.
func readLoadAverage() ([]float64, error) { return nil, errors.New("no load average on Windows") }

func readUptime() (time.Duration, error) {
	ms, _, _ := procGetTickCount64.Call()
	return time.Duration(ms) * time.Millisecond, nil
}
