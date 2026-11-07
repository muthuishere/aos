package groups

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

// `ps -axo pid=,pcpu=,rss=,user=,comm=` on macOS: the command is a full bundle
// path with spaces in it, which is exactly what a naive field split loses.
const psSample = `
    1   0.0  22016 root     /sbin/launchd
  512   3.4 148992 muthu    /Applications/Google Chrome.app/Contents/MacOS/Google Chrome
  777  12.5  65536 muthu    /usr/bin/node
  901   0.0   4096 _windowserver /System/Library/Frameworks/A.framework/Helper
`

func TestParsePsOutput(t *testing.T) {
	got := parsePsOutput(psSample)
	if len(got) != 4 {
		t.Fatalf("parsed %d rows, want 4: %+v", len(got), got)
	}

	cases := []struct {
		index int
		pid   int
		name  string
		cpu   float64
		mem   float64
		user  string
	}{
		{0, 1, "launchd", 0, 21.5, "root"},
		{1, 512, "Google Chrome", 3.4, 145.5, "muthu"},
		{2, 777, "node", 12.5, 64, "muthu"},
		{3, 901, "Helper", 0, 4, "_windowserver"},
	}
	for _, tc := range cases {
		p := got[tc.index]
		if p.PID != tc.pid || p.Name != tc.name || p.CPU != tc.cpu || p.User != tc.user {
			t.Errorf("row %d = %+v; want pid %d name %q cpu %v user %q", tc.index, p, tc.pid, tc.name, tc.cpu, tc.user)
		}
		if p.MemoryMB != tc.mem {
			t.Errorf("row %d memory = %v MB; want %v", tc.index, p.MemoryMB, tc.mem)
		}
	}
}

func TestParsePsOutputSkipsJunk(t *testing.T) {
	junk := "PID %CPU RSS USER COMMAND\n\n   short line\n  42  1.0  1024 me /bin/sh\n"
	got := parsePsOutput(junk)
	if len(got) != 1 || got[0].PID != 42 {
		t.Fatalf("want only the one real row, got %+v", got)
	}
}

var sampleProcesses = []processInfo{
	{PID: 1, Name: "launchd"},
	{PID: 300, Name: "Code"},
	{PID: 301, Name: "Code Helper"},
	{PID: 302, Name: "Code Helper"},
	{PID: 512, Name: "Google Chrome"},
	{PID: 777, Name: "node"},
}

func TestMatchProcesses(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  []int
	}{
		{"case does not matter", "chrome", []int{512}},
		{"substring anywhere in the name", "ode", []int{300, 301, 302, 777}},
		{"surrounding space is not the caller's mistake", "  node ", []int{777}},
		{"a miss stays a miss", "safari", nil},
		{"empty is not a wildcard", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []int
			for _, p := range matchProcesses(sampleProcesses, tc.query) {
				got = append(got, p.PID)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("matchProcesses(%q) = %v; want %v", tc.query, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("matchProcesses(%q) = %v; want %v", tc.query, got, tc.want)
				}
			}
		})
	}
}

func TestResolveKillTarget(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		wantPID  int
		wantErr  bool
		contains []string // fragments the refusal must name
	}{
		{name: "one match resolves", query: "chrome", wantPID: 512},
		{
			// The whole point of the guard: "Code" also substring-matches the
			// two helpers, and killing one of those instead would be silent.
			name:  "an exact name beats its own substring matches",
			query: "Code", wantPID: 300,
		},
		{name: "exact match is case-insensitive too", query: "google chrome", wantPID: 512},
		{
			name:  "an ambiguous name is refused with the candidates",
			query: "Code Helper", wantErr: true,
			contains: []string{"301", "302", "Code Helper"},
		},
		{
			name:  "an ambiguous substring is refused too",
			query: "ode", wantErr: true,
			contains: []string{"300", "301", "302", "777"},
		},
		{name: "no match is a failure, not a no-op", query: "safari", wantErr: true, contains: []string{"safari"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveKillTarget(sampleProcesses, tc.query)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveKillTarget(%q) = %+v; want a refusal", tc.query, got)
				}
				var exit *cli.ExitError
				if !errors.As(err, &exit) || exit.Code != 1 {
					t.Fatalf("want an ExitError with code 1, got %v", err)
				}
				for _, want := range tc.contains {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal %q does not mention %q", err.Error(), want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveKillTarget(%q): %v", tc.query, err)
			}
			if got.PID != tc.wantPID {
				t.Fatalf("resolveKillTarget(%q) = pid %d; want %d", tc.query, got.PID, tc.wantPID)
			}
		})
	}
}

func TestGuardKill(t *testing.T) {
	cases := []struct {
		name    string
		pid     int
		self    int
		wantErr string
	}{
		{"an ordinary pid passes", 4821, 999, ""},
		{"pid 1 takes the machine down with it", 1, 999, "pid 1"},
		{"killing aos would kill the command mid-run", 999, 999, "aos itself"},
		{"zero is not a pid", 0, 999, "not a valid pid"},
		{"a negative pid is a process group, not a process", -1, 999, "not a valid pid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardKill(tc.pid, tc.self)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("guardKill(%d, %d) = %v; want nil", tc.pid, tc.self, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("guardKill(%d, %d) = %v; want an error mentioning %q", tc.pid, tc.self, err, tc.wantErr)
			}
		})
	}
}

func TestProcessBase(t *testing.T) {
	cases := map[string]string{
		"/usr/bin/node": "node",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome": "Google Chrome",
		`C:\Windows\System32\svchost.exe`:                              "svchost.exe",
		"kworker/2:1":                                                  "2:1",
		"  /bin/sh  ":                                                  "sh",
		"":                                                             "",
	}
	for in, want := range cases {
		if got := processBase(in); got != want {
			t.Errorf("processBase(%q) = %q; want %q", in, got, want)
		}
	}
}

// A loose name can match hundreds of processes; the refusal has to stay
// readable, so it samples them and says how many it left out.
func TestResolveKillTargetCapsTheCandidateList(t *testing.T) {
	var many []processInfo
	for i := 0; i < 40; i++ {
		many = append(many, processInfo{PID: 1000 + i, Name: "Chrome Helper"})
	}
	_, err := resolveKillTarget(many, "helper")
	if err == nil {
		t.Fatal("40 matches must be refused")
	}
	if lines := strings.Count(err.Error(), "\n"); lines > 12 {
		t.Fatalf("refusal is %d lines long; it should be sampled:\n%s", lines, err)
	}
	if !strings.Contains(err.Error(), "and 30 more") {
		t.Errorf("refusal does not say how many were left out:\n%s", err)
	}
	if !strings.Contains(err.Error(), "matches 40 processes") {
		t.Errorf("refusal does not give the real total:\n%s", err)
	}
}

func TestRankProcesses(t *testing.T) {
	all := func() []processInfo {
		return []processInfo{
			{PID: 3, Name: "beta", CPU: 5, MemoryMB: 900},
			{PID: 1, Name: "Alpha", CPU: 50, MemoryMB: 10},
			{PID: 2, Name: "gamma", CPU: 50, MemoryMB: 300},
		}
	}
	pids := func(ps []processInfo) []int {
		var out []int
		for _, p := range ps {
			out = append(out, p.PID)
		}
		return out
	}
	cases := []struct {
		key  string
		top  int
		want []int
	}{
		{"cpu", 0, []int{1, 2, 3}}, // tie on cpu breaks by pid
		{"mem", 2, []int{3, 2}},
		{"name", 0, []int{1, 3, 2}},
		{"pid", 0, []int{1, 2, 3}},
		{"", 1, []int{1}},       // --top alone ranks by cpu
		{"", 0, []int{3, 1, 2}}, // nothing asked: order untouched
		{"cpu", 10, []int{1, 2, 3}},
	}
	for _, tc := range cases {
		got, err := rankProcesses(all(), tc.key, tc.top)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(pids(got)) != fmt.Sprint(tc.want) {
			t.Errorf("rank(%q, %d) = %v, want %v", tc.key, tc.top, pids(got), tc.want)
		}
	}
	if _, err := rankProcesses(all(), "size", 0); err == nil {
		t.Error("unknown sort key should fail")
	}
	if _, err := rankProcesses(all(), "cpu", -1); err == nil {
		t.Error("negative top should fail")
	}
}

func TestCPUPercent(t *testing.T) {
	cases := []struct{ before, after, elapsed, want float64 }{
		{10, 10.25, 0.5, 50},
		{10, 11, 0.5, 200}, // two cores busy
		{0, 0.1, 0.5, 20},  // started between readings
		{5, 1, 0.5, 0},     // pid reused
		{1, 2, 0, 0},
	}
	for _, c := range cases {
		if got := cpuPercent(c.before, c.after, c.elapsed); got != c.want {
			t.Errorf("cpuPercent(%v,%v,%v) = %v, want %v", c.before, c.after, c.elapsed, got, c.want)
		}
	}
}
