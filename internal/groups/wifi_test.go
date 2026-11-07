package groups

import (
	"strings"
	"testing"
)

func TestParseWifiDevice(t *testing.T) {
	out := "Hardware Port: Ethernet\nDevice: en3\n\nHardware Port: Wi-Fi\nDevice: en0\nEthernet Address: aa\n"
	if got := parseWifiDevice(out); got != "en0" {
		t.Errorf("got %q", got)
	}
	if got := parseWifiDevice("Hardware Port: Ethernet\nDevice: en3\n"); got != "" {
		t.Errorf("no wifi: got %q", got)
	}
}

func TestReadSecretLine(t *testing.T) {
	for in, want := range map[string]string{"s3cret\n": "s3cret", "s3cret\r\n": "s3cret", "s3cret": "s3cret", "": ""} {
		got, err := readSecretLine(strings.NewReader(in))
		if err != nil || got != want {
			t.Errorf("readSecretLine(%q) = %q, %v", in, got, err)
		}
	}
}

func TestNmcliPasswdFile(t *testing.T) {
	if got := nmcliPasswdFile("p:w"); got != "802-11-wireless-security.psk:p:w\n" {
		t.Errorf("got %q", got)
	}
}

func TestWlanProfileXMLEscapes(t *testing.T) {
	x := wlanProfileXML(`Cafe & "Bar"`, "a<b>")
	if !strings.Contains(x, "<name>Cafe &amp; &quot;Bar&quot;</name>") || !strings.Contains(x, "<keyMaterial>a&lt;b&gt;</keyMaterial>") {
		t.Errorf("not escaped:\n%s", x)
	}
	if !strings.Contains(x, "WPA2PSK") {
		t.Error("want WPA2PSK")
	}
}
