package groups

/*
#include <mach/mach_host.h>
#include <mach/processor_info.h>
#include <mach/vm_map.h>
*/
import "C"

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/muthuishere/aos/internal/sys"
)

// readCPUTicks asks the kernel for every core's user/system/idle/nice ticks.
func readCPUTicks() ([]cpuTicks, error) {
	var count C.natural_t
	var info C.processor_info_array_t
	var infoCount C.mach_msg_type_number_t
	if kr := C.host_processor_info(C.mach_host_self(), C.PROCESSOR_CPU_LOAD_INFO, &count, &info, &infoCount); kr != C.KERN_SUCCESS {
		return nil, fmt.Errorf("host_processor_info failed: %d", kr)
	}
	defer C.vm_deallocate(C.mach_task_self_, C.vm_address_t(uintptr(unsafe.Pointer(info))), C.vm_size_t(uintptr(infoCount)*unsafe.Sizeof(C.integer_t(0))))
	raw := unsafe.Slice((*C.integer_t)(unsafe.Pointer(info)), int(infoCount))
	out := make([]cpuTicks, int(count))
	for i := range out {
		base := i * C.CPU_STATE_MAX
		out[i] = cpuTicks{
			Busy: uint64(uint32(raw[base+C.CPU_STATE_USER])) + uint64(uint32(raw[base+C.CPU_STATE_SYSTEM])) + uint64(uint32(raw[base+C.CPU_STATE_NICE])),
			Idle: uint64(uint32(raw[base+C.CPU_STATE_IDLE])),
		}
	}
	return out, nil
}

func readLoadAverage() ([]float64, error) {
	out, err := sys.Output("sysctl", "-n", "vm.loadavg")
	if err != nil {
		return nil, err
	}
	return parseLoadAvg(out)
}

func readUptime() (time.Duration, error) {
	out, err := sys.Output("sysctl", "-n", "kern.boottime")
	if err != nil {
		return 0, err
	}
	boot, err := parseBoottime(out)
	if err != nil {
		return 0, err
	}
	return time.Since(boot), nil
}
