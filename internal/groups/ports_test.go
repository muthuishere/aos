package groups

import (
	"bytes"
	"encoding/binary"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/muthuishere/aos/internal/cli"
)

func TestParseLsofListen(t *testing.T) {
	got := parseLsofListen("p491\nccrypto-desk\nf3\nn127.0.0.1:21102\np539\ncrapportd\nf8\nn*:58966\nf17\nn[::1]:58966\n")
	want := []listenPort{
		{Port: 21102, Address: "127.0.0.1", PID: 491, Process: "crypto-desk"},
		{Port: 58966, Address: "*", PID: 539, Process: "rapportd"},
		{Port: 58966, Address: "::1", PID: 539, Process: "rapportd"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseSSListen(t *testing.T) {
	got := parseSSListen(`LISTEN 0 4096 127.0.0.1:631 0.0.0.0:* users:(("cupsd",pid=812,fd=7))
LISTEN 0 511 *:80 *:* users:(("nginx",pid=900,fd=6),("nginx",pid=901,fd=6))
LISTEN 0 128 [::]:22 [::]:*
`)
	if len(got) != 4 {
		t.Fatalf("got %+v", got)
	}
	if got[0] != (listenPort{Port: 631, Address: "127.0.0.1", PID: 812, Process: "cupsd"}) {
		t.Errorf("cupsd = %+v", got[0])
	}
	if got[2].PID != 901 || got[2].Port != 80 {
		t.Errorf("second nginx worker = %+v", got[2])
	}
	if got[3] != (listenPort{Port: 22, Address: "::"}) {
		t.Errorf("no-owner row = %+v", got[3])
	}
}

func TestParseTCPTable(t *testing.T) {
	v4 := make([]byte, 4+24)
	binary.LittleEndian.PutUint32(v4, 1)
	copy(v4[4+4:], []byte{127, 0, 0, 1})
	v4[4+8], v4[4+9] = 0x0b, 0xb8 // 3000, network order
	binary.LittleEndian.PutUint32(v4[4+20:], 4242)
	got := parseTCPTable(v4, false)
	if len(got) != 1 || got[0] != (listenPort{Port: 3000, Address: "127.0.0.1", PID: 4242}) {
		t.Errorf("v4 = %+v", got)
	}
	v6 := make([]byte, 4+56)
	binary.LittleEndian.PutUint32(v6, 1)
	v6[4+20], v6[4+21] = 0x1f, 0x90 // 8080
	binary.LittleEndian.PutUint32(v6[4+52:], 7)
	got = parseTCPTable(v6, true)
	if len(got) != 1 || got[0] != (listenPort{Port: 8080, Address: "*", PID: 7}) {
		t.Errorf("v6 = %+v", got)
	}
}

func TestPortOwnersAndDedupe(t *testing.T) {
	ports := []listenPort{
		{Port: 80, Address: "*", PID: 901, Process: "nginx"},
		{Port: 80, Address: "*", PID: 900, Process: "nginx"},
		{Port: 80, Address: "::", PID: 900, Process: "nginx"},
		{Port: 80, Address: "*", PID: 900, Process: "nginx"},
		{Port: 22, Address: "*", PID: 1, Process: "sshd"},
	}
	if d := dedupePorts(ports); len(d) != 4 || d[0].Port != 22 {
		t.Errorf("dedupe = %+v", d)
	}
	owners := portOwners(ports, 80)
	if len(owners) != 2 || owners[0].PID != 900 || owners[1].PID != 901 {
		t.Errorf("owners = %+v", owners)
	}
}

func TestParsePort(t *testing.T) {
	if p, err := parsePort(":3000"); err != nil || p != 3000 {
		t.Errorf("got %d %v", p, err)
	}
	for _, bad := range []string{"0", "70000", "http", ""} {
		if _, err := parsePort(bad); err == nil {
			t.Errorf("parsePort(%q) should fail", bad)
		}
	}
}

// End to end on this machine: start a throwaway listener, find it by port,
// kill it by port.
func TestKillByPortLive(t *testing.T) {
	if testing.Short() {
		t.Skip("starts and kills a process")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("needs python3 for a throwaway listener")
	}
	cmd := exec.Command(py, "-c", "import socket,time;s=socket.socket();s.bind(('127.0.0.1',0));s.listen();print(s.getsockname()[1],flush=True);time.sleep(60)")
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	buf := make([]byte, 16)
	n, _ := stdout.Read(buf)
	port := strings.TrimSpace(string(buf[:n]))

	var out bytes.Buffer
	if err := runNetworkPorts(&cli.Ctx{Stdout: &out}, []string{port}); err != nil {
		t.Fatalf("listener on %s not found: %v", port, err)
	}
	if !strings.Contains(out.String(), strconv.Itoa(cmd.Process.Pid)) {
		t.Errorf("pid %d missing from %q", cmd.Process.Pid, out.String())
	}
	out.Reset()
	if err := killByPort(&cli.Ctx{Stdout: &out}, port, false); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("listener should have been killed")
	}
}
