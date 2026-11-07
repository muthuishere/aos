package groups

import "testing"

func TestParseTrace(t *testing.T) {
	got := parseTrace("fl=123\nh=1.1.1.1\nip=203.0.113.7\nts=1.2\nloc=IN\ncolo=MAA\n")
	if got.IP != "203.0.113.7" || got.Country != "IN" || got.Colo != "MAA" || got.Source != publicIPURL {
		t.Errorf("got %+v", got)
	}
	if parseTrace("<html>blocked</html>").IP != "" {
		t.Error("a non-trace page must yield no ip")
	}
}

func TestParseScutilNC(t *testing.T) {
	got := parseScutilNC(`Available network connection services in the current set (*=enabled):
* (Connected)      6B1C2D3E-0000 PPP --> L2TP       "Work VPN"                       [PPP/L2TP]
* (Disconnected)   7A8B9C0D-1111 IPSec              "Home (IPSec)"                   [IPSec]
`)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0] != (vpnConn{Name: "Work VPN", Connected: true, Kind: "PPP/L2TP"}) {
		t.Errorf("row 0 = %+v", got[0])
	}
	if got[1] != (vpnConn{Name: "Home (IPSec)", Connected: false, Kind: "IPSec"}) {
		t.Errorf("row 1 = %+v (parens in the name must not confuse the status)", got[1])
	}
}

func TestParseNmcliVPN(t *testing.T) {
	got := parseNmcliVPN("Wired connection 1:802-3-ethernet:activated\noffice\\:vpn:vpn:activated\nwg0:wireguard:\n")
	if len(got) != 2 || got[0] != (vpnConn{Name: "office:vpn", Kind: "vpn", Connected: true}) || got[1].Connected {
		t.Errorf("got %+v", got)
	}
}

func TestParseRasdial(t *testing.T) {
	got := parseRasdial("Connected to\r\nWork VPN\r\nCommand completed successfully.\r\n")
	if len(got) != 1 || got[0] != (vpnConn{Name: "Work VPN", Connected: true}) {
		t.Errorf("got %+v", got)
	}
	if got := parseRasdial("No connections\r\nCommand completed successfully.\r\n"); len(got) != 0 {
		t.Errorf("no connections: %+v", got)
	}
}
