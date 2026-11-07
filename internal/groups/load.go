package groups

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// cpuTicks is one core's cumulative time counters: busy plus idle is total.
type cpuTicks struct{ Busy, Idle uint64 }

// loadInfo is what `system load --json` emits.
type loadInfo struct {
	CPUPercent float64   `json:"cpu_percent"`
	PerCore    []float64 `json:"per_core_percent"`
	Cores      int       `json:"cores"`
	// LoadAverage is the 1, 5 and 15 minute run-queue averages. Windows has
	// no such counter, so it is omitted there.
	LoadAverage []float64 `json:"load_average,omitempty"`
}

// uptimeInfo is what `system uptime --json` emits.
type uptimeInfo struct {
	Seconds int64  `json:"seconds"`
	Human   string `json:"human"`
	BootAt  string `json:"boot_at"`
}

// loadSample is how long `system load` watches the counters. Long enough to
// be meaningful, short enough that an agent call stays quick.
const loadSample = 500 * time.Millisecond

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "system", Name: "load",
				Summary: "Print CPU busy % overall and per core, and load averages",
				Args:    "[--json]",
				Examples: []string{
					"aos system load",
					"aos system load --json",
				},
				Run: runSystemLoad,
			},
			&cli.Command{
				Group: "system", Name: "uptime",
				Summary: "Print how long the machine has been up and when it booted",
				Args:    "[--json]",
				Examples: []string{
					"aos system uptime",
					"aos system uptime --json",
				},
				Run: runSystemUptime,
			},
		)
	})
}

func runSystemLoad(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("system load", args)
	if err != nil {
		return err
	}
	before, err := readCPUTicks()
	if err != nil {
		return err
	}
	time.Sleep(loadSample)
	after, err := readCPUTicks()
	if err != nil {
		return err
	}
	info := loadInfo{}
	info.CPUPercent, info.PerCore = cpuPercents(before, after)
	info.Cores = len(info.PerCore)
	info.LoadAverage, _ = readLoadAverage()
	if asJSON {
		return writeJSON(c, info)
	}
	c.Printf("cpu       %.1f%%  (%d cores)\n", info.CPUPercent, info.Cores)
	if len(info.LoadAverage) == 3 {
		c.Printf("load avg  %.2f  %.2f  %.2f  (1, 5, 15 min)\n", info.LoadAverage[0], info.LoadAverage[1], info.LoadAverage[2])
	}
	cores := make([]string, len(info.PerCore))
	for i, p := range info.PerCore {
		cores[i] = fmt.Sprintf("%.0f", p)
	}
	c.Printf("per core  %s\n", strings.Join(cores, " "))
	return nil
}

func runSystemUptime(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("system uptime", args)
	if err != nil {
		return err
	}
	up, err := readUptime()
	if err != nil {
		return err
	}
	info := uptimeInfo{
		Seconds: int64(up / time.Second),
		Human:   humanDuration(up),
		BootAt:  time.Now().Add(-up).Truncate(time.Second).Format(time.RFC3339),
	}
	if asJSON {
		return writeJSON(c, info)
	}
	c.Printf("up %s (since %s)\n", info.Human, info.BootAt)
	return nil
}

// jsonOnly parses the args of a command whose only flag is --json.
func jsonOnly(route string, args []string) (bool, error) {
	set, err := parseArgs(args)
	if err != nil {
		return false, err
	}
	if err := set.Reject("json"); err != nil {
		return false, err
	}
	if len(set.Rest) > 0 {
		return false, fmt.Errorf("`%s` takes no positional arguments", route)
	}
	return set.Has("json"), nil
}

// cpuPercents turns two counter samples into busy percentages, overall and per
// core, rounded to one decimal.
func cpuPercents(before, after []cpuTicks) (float64, []float64) {
	n := min(len(before), len(after))
	per := make([]float64, n)
	var busy, total uint64
	for i := 0; i < n; i++ {
		b := sub(after[i].Busy, before[i].Busy)
		t := b + sub(after[i].Idle, before[i].Idle)
		per[i] = percent(b, t)
		busy += b
		total += t
	}
	return percent(busy, total), per
}

func sub(a, b uint64) uint64 {
	if a < b {
		return 0 // a counter wrapped or a core went offline
	}
	return a - b
}

func percent(part, whole uint64) float64 {
	if whole == 0 {
		return 0
	}
	return float64(int(float64(part)*1000/float64(whole)+0.5)) / 10
}

// parseProcStat reads the per-core "cpuN" lines of Linux /proc/stat. Busy is
// user+nice+system+irq+softirq+steal; idle is idle+iowait.
func parseProcStat(text string) []cpuTicks {
	var out []cpuTicks
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) < 8 || !strings.HasPrefix(f[0], "cpu") || f[0] == "cpu" {
			continue
		}
		v := make([]uint64, len(f)-1)
		for i := range v {
			v[i], _ = strconv.ParseUint(f[i+1], 10, 64)
		}
		t := cpuTicks{Busy: v[0] + v[1] + v[2] + v[5] + v[6], Idle: v[3] + v[4]}
		if len(v) > 7 {
			t.Busy += v[7]
		}
		out = append(out, t)
	}
	return out
}

// parseLoadAvg reads the first three numbers of /proc/loadavg or of macOS
// `sysctl -n vm.loadavg` ("{ 6.79 7.02 6.84 }").
func parseLoadAvg(text string) ([]float64, error) {
	var out []float64
	for _, f := range strings.Fields(strings.Trim(strings.TrimSpace(text), "{}")) {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil {
			continue
		}
		out = append(out, v)
		if len(out) == 3 {
			return out, nil
		}
	}
	return nil, fmt.Errorf("no load averages in %q", text)
}

// parseBoottime reads macOS `sysctl -n kern.boottime`:
// "{ sec = 1793192128, usec = 663032 } Wed Oct 28 18:25:28 2026".
func parseBoottime(text string) (time.Time, error) {
	_, after, ok := strings.Cut(text, "sec = ")
	if !ok {
		return time.Time{}, fmt.Errorf("unexpected kern.boottime %q", text)
	}
	sec, err := strconv.ParseInt(strings.TrimRight(strings.Fields(after)[0], ","), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(sec, 0), nil
}

// parseProcUptime reads the first field of Linux /proc/uptime (seconds).
func parseProcUptime(text string) (time.Duration, error) {
	f := strings.Fields(text)
	if len(f) == 0 {
		return 0, fmt.Errorf("empty /proc/uptime")
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, err
	}
	return time.Duration(s * float64(time.Second)), nil
}

// humanDuration renders an uptime as "9d 4h 12m".
func humanDuration(d time.Duration) string {
	m := int64(d / time.Minute)
	days, hours, mins := m/(24*60), (m/60)%24, m%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// windowsTicks converts processor times, where kernel time includes idle.
func windowsTicks(idle, kernel, user int64) cpuTicks {
	busy := kernel + user - idle
	if busy < 0 {
		busy = 0
	}
	return cpuTicks{Busy: uint64(busy), Idle: uint64(max(idle, 0))}
}
