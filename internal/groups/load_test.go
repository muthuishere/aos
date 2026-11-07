package groups

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func TestCPUPercents(t *testing.T) {
	before := []cpuTicks{{Busy: 100, Idle: 100}, {Busy: 50, Idle: 50}}
	after := []cpuTicks{{Busy: 175, Idle: 125}, {Busy: 50, Idle: 150}}
	all, per := cpuPercents(before, after)
	if per[0] != 75 || per[1] != 0 || all != 37.5 {
		t.Errorf("got %v %v", all, per)
	}
	// A counter that went backwards counts as nothing, not a huge number.
	if _, per := cpuPercents([]cpuTicks{{Busy: 10, Idle: 10}}, []cpuTicks{{Busy: 5, Idle: 20}}); per[0] != 0 {
		t.Errorf("wrapped counter: %v", per)
	}
}

func TestParseProcStat(t *testing.T) {
	ticks := parseProcStat(`cpu  10 0 10 80 0 0 0 0 0 0
cpu0 1 2 3 4 5 6 7 8 0 0
cpu1 10 0 10 80 0 0 0 0 0 0
intr 12345
`)
	if len(ticks) != 2 {
		t.Fatalf("want 2 cores, got %v", ticks)
	}
	if ticks[0] != (cpuTicks{Busy: 1 + 2 + 3 + 6 + 7 + 8, Idle: 4 + 5}) {
		t.Errorf("core0 = %+v", ticks[0])
	}
}

func TestParseLoadAvg(t *testing.T) {
	for _, in := range []string{"{ 6.79 7.02 6.84 }", "6.79 7.02 6.84 2/1234 5678\n"} {
		got, err := parseLoadAvg(in)
		if err != nil || len(got) != 3 || got[0] != 6.79 || got[2] != 6.84 {
			t.Errorf("parseLoadAvg(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := parseLoadAvg("nothing"); err == nil {
		t.Error("want error")
	}
}

func TestParseBoottime(t *testing.T) {
	got, err := parseBoottime("{ sec = 1793192128, usec = 663032 } Wed Oct 28 18:25:28 2026")
	if err != nil || got.Unix() != 1793192128 {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestParseProcUptime(t *testing.T) {
	got, err := parseProcUptime("3600.50 7000.00\n")
	if err != nil || got != 3600500*time.Millisecond {
		t.Errorf("got %v, %v", got, err)
	}
}

func TestWindowsTicks(t *testing.T) {
	if got := windowsTicks(60, 80, 20); got != (cpuTicks{Busy: 40, Idle: 60}) {
		t.Errorf("got %+v", got)
	}
}

func TestHumanDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		5 * time.Minute:                            "5m",
		2*time.Hour + 3*time.Minute:                "2h 3m",
		9*24*time.Hour + 4*time.Hour + time.Minute: "9d 4h 1m",
	} {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestSystemLoadAndUptimeLive(t *testing.T) {
	var out bytes.Buffer
	if err := runSystemLoad(&cli.Ctx{Stdout: &out}, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var l loadInfo
	if err := json.Unmarshal(out.Bytes(), &l); err != nil {
		t.Fatal(err)
	}
	if l.Cores == 0 || l.CPUPercent < 0 || l.CPUPercent > 100 {
		t.Errorf("implausible load: %+v", l)
	}
	out.Reset()
	if err := runSystemUptime(&cli.Ctx{Stdout: &out}, []string{"--json"}); err != nil {
		t.Fatal(err)
	}
	var u uptimeInfo
	if err := json.Unmarshal(out.Bytes(), &u); err != nil || u.Seconds <= 0 {
		t.Errorf("implausible uptime: %+v %v", u, err)
	}
}
