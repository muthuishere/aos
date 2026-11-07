package groups

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

func TestParseMeminfo(t *testing.T) {
	m := parseMeminfo(`MemTotal:       16000000 kB
MemFree:         1000000 kB
MemAvailable:    6000000 kB
Buffers:          200000 kB
Cached:          3000000 kB
SwapTotal:       2000000 kB
SwapFree:        1500000 kB
`)
	m.finish()
	if m.TotalBytes != 16000000*1024 || m.AvailableBytes != 6000000*1024 || m.FreeBytes != 1000000*1024 {
		t.Fatalf("bad memory: %+v", m)
	}
	if m.UsedBytes != 10000000*1024 {
		t.Errorf("used = %d", m.UsedBytes)
	}
	if m.UsedPercent != 62.5 {
		t.Errorf("used%% = %v", m.UsedPercent)
	}
	if m.SwapTotalBytes != 2000000*1024 || m.SwapUsedBytes != 500000*1024 {
		t.Errorf("swap = %d/%d", m.SwapUsedBytes, m.SwapTotalBytes)
	}
}

func TestParseMeminfoOldKernel(t *testing.T) {
	m := parseMeminfo("MemTotal: 100 kB\nMemFree: 10 kB\nBuffers: 5 kB\nCached: 20 kB\n")
	if m.AvailableBytes != 35*1024 {
		t.Errorf("available = %d, want free+buffers+cached", m.AvailableBytes)
	}
}

func TestDarwinMemory(t *testing.T) {
	size, pages := parseVMStat(`Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                                6820.
Pages speculative:                         5406.
Pages wired down:                        292386.
Pages purgeable:                           6961.
"Translation faults":               19064285728.
Anonymous pages:                        1436130.
Pages occupied by compressor:            689359.
`)
	if size != 16384 {
		t.Fatalf("page size = %d", size)
	}
	if pages["Translation faults"] != 19064285728 {
		t.Errorf("quoted label not parsed: %v", pages)
	}
	m := darwinMemory(51539607552, size, pages)
	wantUsed := uint64(1436130-6961+292386+689359) * 16384
	if m.UsedBytes != wantUsed || m.AvailableBytes != 51539607552-wantUsed {
		t.Errorf("used = %d, want %d", m.UsedBytes, wantUsed)
	}
	if m.FreeBytes != (6820+5406)*16384 {
		t.Errorf("free = %d", m.FreeBytes)
	}
}

func TestParseSwapUsage(t *testing.T) {
	total, used := parseSwapUsage("total = 5120.00M  used = 3917.50M  free = 1202.50M  (encrypted)")
	if total != 5120<<20 || used != uint64(3917.5*(1<<20)) {
		t.Errorf("swap = %d/%d", used, total)
	}
}

func TestPressureName(t *testing.T) {
	for in, want := range map[string]string{"1": "normal", "2": "warn", "4": "critical", "9": ""} {
		if got := pressureName(in); got != want {
			t.Errorf("pressureName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWindowsMemory(t *testing.T) {
	const gb = 1 << 30
	m := windowsMemory(16*gb, 6*gb, 20*gb, 8*gb)
	m.finish()
	if m.UsedBytes != 10*gb || m.SwapTotalBytes != 4*gb || m.SwapUsedBytes != 2*gb {
		t.Errorf("bad: %+v", m)
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[uint64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 16 << 30: "16.0 GB", 3 << 40: "3.0 TB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestSystemMemoryLive(t *testing.T) {
	var out bytes.Buffer
	c := &cli.Ctx{Stdout: &out}
	if err := runSystemMemory(c, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var m memoryInfo
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.TotalBytes == 0 || m.UsedBytes > m.TotalBytes {
		t.Errorf("implausible reading: %+v", m)
	}
}
