package groups

import (
	"encoding/binary"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// listenPort is one listening TCP socket, in the shape `network ports --json`
// emits. Process is "" when the OS would not say (another user's process
// without the rights to see it).
type listenPort struct {
	Port    int    `json:"port"`
	Address string `json:"address"`
	PID     int    `json:"pid"`
	Process string `json:"process,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "network", Name: "ports",
			Summary: "List listening TCP ports with the pid and process that own them",
			Args:    "[<port>] [--json]",
			Examples: []string{
				"aos network ports",
				"aos network ports 3000          # who is on port 3000; exit 1 if nobody",
				"aos network ports --json",
			},
			Run: runNetworkPorts,
		})
	})
}

func runNetworkPorts(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	if len(set.Rest) > 1 {
		return fmt.Errorf("`network ports` takes at most one port")
	}
	ports, err := listListening()
	if err != nil {
		return err
	}
	ports = dedupePorts(ports)
	if len(set.Rest) == 1 {
		port, err := parsePort(set.Rest[0])
		if err != nil {
			return err
		}
		ports = portsOn(ports, port)
		if len(ports) == 0 {
			if set.Has("json") {
				_ = writeJSON(c, []listenPort{})
			}
			return &cli.ExitError{Code: 1, Message: fmt.Sprintf("nothing is listening on port %d", port)}
		}
	}
	if set.Has("json") {
		if ports == nil {
			ports = []listenPort{}
		}
		return writeJSON(c, ports)
	}
	for _, p := range ports {
		c.Printf("%5d  %-22s %7d  %s\n", p.Port, p.Address, p.PID, p.Process)
	}
	return nil
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(s, ":"))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("%q is not a port number (1-65535)", s)
	}
	return n, nil
}

func portsOn(all []listenPort, port int) []listenPort {
	var out []listenPort
	for _, p := range all {
		if p.Port == port {
			out = append(out, p)
		}
	}
	return out
}

// dedupePorts drops repeats (one socket per IPv4 and IPv6 address family, or
// the same socket shared by forked workers listed once per fd) and sorts by
// port, then pid.
func dedupePorts(all []listenPort) []listenPort {
	seen := map[string]bool{}
	var out []listenPort
	for _, p := range all {
		key := fmt.Sprintf("%d|%s|%d", p.Port, p.Address, p.PID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].PID < out[j].PID
	})
	return out
}

// splitHostPort splits "127.0.0.1:3000", "*:3000", "[::1]:3000" or
// "[::]:3000" at the last colon.
func splitHostPort(s string) (string, int, bool) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, false
	}
	return strings.Trim(s[:i], "[]"), port, true
}

// parseLsofListen reads `lsof -nP -iTCP -sTCP:LISTEN -F pcn`: a p line starts
// a process, c names it, every n line is one listening address.
func parseLsofListen(out string) []listenPort {
	var found []listenPort
	pid, name := 0, ""
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
			name = ""
		case 'c':
			name = line[1:]
		case 'n':
			if host, port, ok := splitHostPort(line[1:]); ok {
				found = append(found, listenPort{Port: port, Address: host, PID: pid, Process: name})
			}
		}
	}
	return found
}

// parseSSListen reads `ss -Hltnp`:
//
//	LISTEN 0 4096 127.0.0.1:631 0.0.0.0:* users:(("cupsd",pid=812,fd=7))
func parseSSListen(out string) []listenPort {
	var found []listenPort
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		host, port, ok := splitHostPort(f[3])
		if !ok {
			continue
		}
		users := ""
		if len(f) > 5 {
			users = strings.Join(f[5:], " ")
		}
		owners := ssUsers(users)
		if len(owners) == 0 {
			found = append(found, listenPort{Port: port, Address: host})
		}
		for _, o := range owners {
			o.Port, o.Address = port, host
			found = append(found, o)
		}
	}
	return found
}

// ssUsers pulls ("name",pid=N,...) tuples out of ss's users:(...) column.
func ssUsers(s string) []listenPort {
	var out []listenPort
	for {
		i := strings.Index(s, `("`)
		if i < 0 {
			return out
		}
		s = s[i+2:]
		end := strings.Index(s, `"`)
		if end < 0 {
			return out
		}
		name := s[:end]
		s = s[end:]
		j := strings.Index(s, "pid=")
		if j < 0 {
			return out
		}
		digits := s[j+4:]
		k := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' })
		if k < 0 {
			k = len(digits)
		}
		pid, _ := strconv.Atoi(digits[:k])
		out = append(out, listenPort{PID: pid, Process: name})
		s = digits[k:]
	}
}

// parseTCPTable decodes MIB_TCPTABLE_OWNER_PID / MIB_TCP6TABLE_OWNER_PID.
// Ports are in network byte order in the low 16 bits of a DWORD.
func parseTCPTable(buf []byte, v6 bool) []listenPort {
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf))
	rowSize, off := 24, 4
	if v6 {
		rowSize = 56
	}
	var out []listenPort
	for i := 0; i < n && off+rowSize <= len(buf); i, off = i+1, off+rowSize {
		row := buf[off : off+rowSize]
		var addr net.IP
		var portField []byte
		var pid uint32
		if v6 {
			addr = net.IP(append([]byte(nil), row[0:16]...))
			portField = row[20:24]
			pid = binary.LittleEndian.Uint32(row[52:56])
		} else {
			addr = net.IPv4(row[4], row[5], row[6], row[7])
			portField = row[8:12]
			pid = binary.LittleEndian.Uint32(row[20:24])
		}
		port := int(portField[0])<<8 | int(portField[1])
		host := addr.String()
		if addr.IsUnspecified() {
			host = "*"
		}
		out = append(out, listenPort{Port: port, Address: host, PID: int(pid)})
	}
	return out
}
