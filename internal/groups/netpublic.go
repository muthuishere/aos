package groups

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// publicIPInfo is what `network public-ip --json` emits.
type publicIPInfo struct {
	IP      string `json:"ip"`
	Country string `json:"country,omitempty"`
	Colo    string `json:"colo,omitempty"` // the Cloudflare data centre that answered
	Source  string `json:"source"`
}

// vpnConn is one VPN the OS knows about, in `network vpn status --json`.
type vpnConn struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Kind      string `json:"kind,omitempty"`
}

// publicIPURL is Cloudflare's trace endpoint: no key, no account, plain
// key=value text, reached by IP so a broken DNS still gets an answer.
const publicIPURL = "https://1.1.1.1/cdn-cgi/trace"

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "network", Name: "public-ip",
				Summary: "Print this machine's public IP address as the internet sees it",
				Args:    "[--json]",
				Examples: []string{
					"aos network public-ip",
					"aos network public-ip --json   # ip, country, source",
				},
				Run: runNetworkPublicIP,
			},
			&cli.Command{
				Group: "network", Name: "vpn status",
				Summary: "List the system's VPN connections and which are connected",
				Args:    "[--json]",
				Examples: []string{
					"aos network vpn status",
					"aos network vpn status --json",
				},
				Run: runVPNStatus,
			},
			&cli.Command{
				Group: "network", Name: "vpn on",
				Summary:  "Connect a system VPN by name",
				Args:     "<name>",
				Examples: []string{`aos network vpn on "Work VPN"`},
				Run:      func(c *cli.Ctx, args []string) error { return runVPNSwitch(c, args, true) },
			},
			&cli.Command{
				Group: "network", Name: "vpn off",
				Summary:  "Disconnect a system VPN by name",
				Args:     "<name>",
				Examples: []string{`aos network vpn off "Work VPN"`},
				Run:      func(c *cli.Ctx, args []string) error { return runVPNSwitch(c, args, false) },
			},
		)
	})
}

func runNetworkPublicIP(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("network public-ip", args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, publicIPURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "aos")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("could not reach %s: %v (offline?)", publicIPURL, err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	info := parseTrace(string(body))
	if info.IP == "" {
		return &cli.ExitError{Code: 1, Message: "no ip in the answer from " + publicIPURL}
	}
	if asJSON {
		return writeJSON(c, info)
	}
	c.Println(info.IP)
	return nil
}

// parseTrace reads Cloudflare's /cdn-cgi/trace key=value lines.
func parseTrace(text string) publicIPInfo {
	info := publicIPInfo{Source: publicIPURL}
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ip":
			info.IP = v
		case "loc":
			info.Country = v
		case "colo":
			info.Colo = v
		}
	}
	return info
}

func runVPNStatus(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("network vpn status", args)
	if err != nil {
		return err
	}
	conns, err := listVPNs()
	if err != nil {
		return err
	}
	if asJSON {
		if conns == nil {
			conns = []vpnConn{}
		}
		return writeJSON(c, conns)
	}
	if len(conns) == 0 {
		c.Println("no system VPN configured (app VPNs that bypass the OS VPN settings are not listed)")
		return nil
	}
	for _, v := range conns {
		state := "disconnected"
		if v.Connected {
			state = "connected"
		}
		c.Printf("%-13s %s\n", state, v.Name)
	}
	return nil
}

func runVPNSwitch(c *cli.Ctx, args []string, on bool) error {
	if len(args) != 1 || strings.HasPrefix(args[0], "--") {
		return fmt.Errorf("give the VPN's name as `network vpn status` lists it, e.g. \"Work VPN\"")
	}
	name := args[0]
	if err := switchVPN(name, on); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	if on {
		c.Printf("connecting %s\n", name)
	} else {
		c.Printf("disconnected %s\n", name)
	}
	return nil
}

// parseScutilNC reads macOS `scutil --nc list`:
//
//   - (Connected)      6B1C…  PPP --> L2TP       "Work VPN"     [PPP/L2TP]
func parseScutilNC(out string) []vpnConn {
	var found []vpnConn
	for _, line := range strings.Split(out, "\n") {
		open, close := strings.Index(line, "("), strings.Index(line, ")")
		q1 := strings.Index(line, `"`)
		if open < 0 || close < open || q1 < 0 {
			continue
		}
		q2 := strings.Index(line[q1+1:], `"`)
		if q2 < 0 {
			continue
		}
		v := vpnConn{
			Name:      line[q1+1 : q1+1+q2],
			Connected: line[open+1:close] == "Connected",
		}
		if b := strings.LastIndex(line, "["); b > q1 {
			v.Kind = strings.Trim(strings.TrimSpace(line[b:]), "[]")
		}
		found = append(found, v)
	}
	return found
}

// parseNmcliVPN reads `nmcli -t -f NAME,TYPE,STATE connection show`, keeping
// VPN and WireGuard profiles. Colons inside a name are escaped as "\:".
func parseNmcliVPN(out string) []vpnConn {
	var found []vpnConn
	for _, line := range strings.Split(out, "\n") {
		f := splitNmcli(line)
		if len(f) < 3 || (f[1] != "vpn" && f[1] != "wireguard") {
			continue
		}
		found = append(found, vpnConn{Name: f[0], Kind: f[1], Connected: f[2] == "activated"})
	}
	return found
}

func splitNmcli(line string) []string {
	var fields []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line):
			i++
			cur.WriteByte(line[i])
		case line[i] == ':':
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(line[i])
		}
	}
	return append(fields, cur.String())
}

// parseRasdial reads `rasdial` with no arguments: the connected entries
// between "Connected to" and the closing status line, or "No connections".
func parseRasdial(out string) []vpnConn {
	var found []vpnConn
	lines := strings.Split(strings.ReplaceAll(out, "\r", ""), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "Connected to" {
			continue
		}
		for _, name := range lines[i+1:] {
			name = strings.TrimSpace(name)
			if name == "" || strings.HasPrefix(name, "Command completed") {
				break
			}
			found = append(found, vpnConn{Name: name, Connected: true})
		}
	}
	return found
}
