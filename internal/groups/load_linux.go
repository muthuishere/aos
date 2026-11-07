package groups

import (
	"os"
	"time"
)

func readCPUTicks() ([]cpuTicks, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, err
	}
	return parseProcStat(string(data)), nil
}

func readLoadAverage() ([]float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return nil, err
	}
	return parseLoadAvg(string(data))
}

func readUptime() (time.Duration, error) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseProcUptime(string(data))
}
