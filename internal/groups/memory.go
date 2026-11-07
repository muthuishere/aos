package groups

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// memoryInfo is one reading of physical memory and swap, in bytes.
//
// Used is what the OS counts as taken by programs and the kernel; Available is
// what a new program could still get without swapping (free plus reclaimable
// cache). Free is the strictly unused part, which is usually small on a healthy
// machine because idle RAM is used as cache — Available is the number to
// answer "how much memory is left" with.
type memoryInfo struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	FreeBytes      uint64  `json:"free_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	SwapTotalBytes uint64  `json:"swap_total_bytes"`
	SwapUsedBytes  uint64  `json:"swap_used_bytes"`
	// Pressure is normal, warn or critical where the OS rates it (macOS),
	// and "" elsewhere.
	Pressure string `json:"pressure,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "system", Name: "memory",
			Summary: "Print total, used, available and free RAM, and swap",
			Args:    "[--json]",
			Examples: []string{
				"aos system memory",
				"aos system memory --json",
			},
			Run: runSystemMemory,
		})
	})
}

func runSystemMemory(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`system memory` takes no positional arguments")
	}
	m, err := readMemory()
	if err != nil {
		return err
	}
	m.finish()
	if set.Has("json") {
		return writeJSON(c, m)
	}
	c.Printf("total      %s\n", humanBytes(m.TotalBytes))
	c.Printf("used       %s (%.0f%%)\n", humanBytes(m.UsedBytes), m.UsedPercent)
	c.Printf("available  %s\n", humanBytes(m.AvailableBytes))
	c.Printf("free       %s\n", humanBytes(m.FreeBytes))
	c.Printf("swap       %s used of %s\n", humanBytes(m.SwapUsedBytes), humanBytes(m.SwapTotalBytes))
	if m.Pressure != "" {
		c.Printf("pressure   %s\n", m.Pressure)
	}
	return nil
}

// finish derives the fields every backend computes the same way.
func (m *memoryInfo) finish() {
	if m.UsedBytes == 0 && m.TotalBytes >= m.AvailableBytes {
		m.UsedBytes = m.TotalBytes - m.AvailableBytes
	}
	if m.AvailableBytes == 0 && m.TotalBytes >= m.UsedBytes {
		m.AvailableBytes = m.TotalBytes - m.UsedBytes
	}
	if m.TotalBytes > 0 {
		pct := float64(m.UsedBytes) * 100 / float64(m.TotalBytes)
		m.UsedPercent = float64(int(pct*10+0.5)) / 10
	}
}

// parseMeminfo reads Linux /proc/meminfo, whose values are in kB.
func parseMeminfo(text string) memoryInfo {
	kb := map[string]uint64{}
	for _, line := range strings.Split(text, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
			kb[strings.TrimSpace(key)] = v * 1024
		}
	}
	m := memoryInfo{
		TotalBytes:     kb["MemTotal"],
		FreeBytes:      kb["MemFree"],
		AvailableBytes: kb["MemAvailable"],
		SwapTotalBytes: kb["SwapTotal"],
	}
	if _, ok := kb["MemAvailable"]; !ok {
		// Kernels before 3.14 lack MemAvailable; free + cache is the old estimate.
		m.AvailableBytes = kb["MemFree"] + kb["Buffers"] + kb["Cached"]
	}
	if kb["SwapTotal"] >= kb["SwapFree"] {
		m.SwapUsedBytes = kb["SwapTotal"] - kb["SwapFree"]
	}
	return m
}

// parseVMStat reads macOS `vm_stat` into page counts keyed by label, and the
// page size from its header line.
func parseVMStat(text string) (pageSize uint64, pages map[string]uint64) {
	pages = map[string]uint64{}
	pageSize = 4096
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "Mach Virtual Memory Statistics") {
			if _, after, ok := strings.Cut(line, "page size of "); ok {
				if v, err := strconv.ParseUint(strings.Fields(after)[0], 10, 64); err == nil {
					pageSize = v
				}
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSuffix(strings.TrimSpace(value), ".")
		if v, err := strconv.ParseUint(value, 10, 64); err == nil {
			pages[strings.Trim(strings.TrimSpace(key), `"`)] = v
		}
	}
	return pageSize, pages
}

// darwinMemory turns vm_stat pages into Activity Monitor's numbers: used is
// app memory (anonymous minus purgeable) plus wired plus compressed.
func darwinMemory(total, pageSize uint64, pages map[string]uint64) memoryInfo {
	app := pages["Anonymous pages"]
	if p := pages["Pages purgeable"]; app >= p {
		app -= p
	}
	used := (app + pages["Pages wired down"] + pages["Pages occupied by compressor"]) * pageSize
	if used > total {
		used = total
	}
	return memoryInfo{
		TotalBytes:     total,
		UsedBytes:      used,
		AvailableBytes: total - used,
		FreeBytes:      (pages["Pages free"] + pages["Pages speculative"]) * pageSize,
	}
}

// parseSwapUsage reads macOS `sysctl -n vm.swapusage`:
// "total = 5120.00M  used = 3917.50M  free = 1202.50M  (encrypted)".
func parseSwapUsage(text string) (total, used uint64) {
	fields := strings.Fields(text)
	for i := 0; i+2 < len(fields); i++ {
		if fields[i+1] != "=" {
			continue
		}
		v := sizeWithSuffix(fields[i+2])
		switch fields[i] {
		case "total":
			total = v
		case "used":
			used = v
		}
	}
	return total, used
}

// sizeWithSuffix parses "5120.00M" style sizes (K, M, G, T; binary multiples).
func sizeWithSuffix(s string) uint64 {
	mult := 1.0
	if n := len(s); n > 0 {
		switch s[n-1] {
		case 'K', 'k':
			mult, s = 1<<10, s[:n-1]
		case 'M', 'm':
			mult, s = 1<<20, s[:n-1]
		case 'G', 'g':
			mult, s = 1<<30, s[:n-1]
		case 'T', 't':
			mult, s = 1<<40, s[:n-1]
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0
	}
	return uint64(v * mult)
}

// pressureName maps kern.memorystatus_vm_pressure_level (1, 2, 4).
func pressureName(level string) string {
	switch strings.TrimSpace(level) {
	case "1":
		return "normal"
	case "2":
		return "warn"
	case "4":
		return "critical"
	}
	return ""
}

// windowsMemory maps GlobalMemoryStatusEx. The "page file" figures there are
// the commit limit, physical RAM plus page files, so swap is the difference.
func windowsMemory(totalPhys, availPhys, totalPage, availPage uint64) memoryInfo {
	m := memoryInfo{TotalBytes: totalPhys, AvailableBytes: availPhys, FreeBytes: availPhys}
	if totalPage > totalPhys {
		m.SwapTotalBytes = totalPage - totalPhys
		usedCommit := totalPage - availPage
		usedPhys := totalPhys - availPhys
		if usedCommit > usedPhys {
			m.SwapUsedBytes = min(usedCommit-usedPhys, m.SwapTotalBytes)
		}
	}
	return m
}
