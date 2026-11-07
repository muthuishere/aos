package groups

import (
	"strconv"

	"github.com/muthuishere/aos/internal/sys"
)

func readMemory() (memoryInfo, error) {
	out, err := sys.Output("sysctl", "-n", "hw.memsize")
	if err != nil {
		return memoryInfo{}, err
	}
	total, err := strconv.ParseUint(out, 10, 64)
	if err != nil {
		return memoryInfo{}, err
	}
	vm, err := sys.Output("vm_stat")
	if err != nil {
		return memoryInfo{}, err
	}
	pageSize, pages := parseVMStat(vm)
	m := darwinMemory(total, pageSize, pages)
	if swap, err := sys.Output("sysctl", "-n", "vm.swapusage"); err == nil {
		m.SwapTotalBytes, m.SwapUsedBytes = parseSwapUsage(swap)
	}
	if level, err := sys.Output("sysctl", "-n", "kern.memorystatus_vm_pressure_level"); err == nil {
		m.Pressure = pressureName(level)
	}
	return m, nil
}
