package groups

import "os"

func readMemory() (memoryInfo, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return memoryInfo{}, err
	}
	return parseMeminfo(string(data)), nil
}
