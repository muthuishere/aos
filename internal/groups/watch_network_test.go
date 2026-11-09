package groups

import (
	"strings"
	"testing"
	"time"
)

func TestNetChange(t *testing.T) {
	off := netState{}
	home := netState{Interface: "en0", IP: "10.0.0.2", SSID: "home"}
	cafe := netState{Interface: "en0", IP: "10.0.0.9", SSID: "cafe"}
	cases := []struct {
		prev, cur netState
		want      string
	}{
		{off, home, "online"},
		{home, off, "offline"},
		{home, cafe, "wifi"},
		{home, netState{Interface: "en0", IP: "10.0.0.3", SSID: "home"}, ""}, // new address only
		{off, off, ""},
		{netState{Interface: "en0"}, home, "online"}, // interface up, address arrives later
	}
	for _, tc := range cases {
		if got := netChange(tc.prev, tc.cur); got != tc.want {
			t.Errorf("%+v -> %+v: got %q want %q", tc.prev, tc.cur, got, tc.want)
		}
	}
}

func TestWatchNetworkLoop(t *testing.T) {
	seqReads := func(states ...netState) func() (netState, error) {
		i := 0
		return func() (netState, error) {
			s := states[min(i, len(states)-1)]
			i++
			return s, nil
		}
	}
	home := netState{Interface: "en0", IP: "10.0.0.2", SSID: "home"}
	cafe := netState{Interface: "en0", IP: "10.0.0.9", SSID: "cafe"}

	// A notification triggers a read; one line per change.
	notify := make(chan struct{}, 4)
	for i := 0; i < 3; i++ {
		notify <- struct{}{}
	}
	c, out := pageCtx()
	err := watchNetworkLoop(c, netWatch{max: 2, interval: time.Hour, notify: notify, read: seqReads(home, home, cafe, netState{})})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if err != nil || len(lines) != 2 || !strings.Contains(lines[0], `"change":"wifi"`) || !strings.Contains(lines[0], `"prev_ssid":"home"`) ||
		!strings.Contains(lines[1], `"change":"offline"`) {
		t.Fatalf("events: %v\n%s", err, out.String())
	}

	// --until a state the machine is already in returns at once.
	c, out = pageCtx()
	if err := watchNetworkLoop(c, netWatch{until: "online", interval: time.Hour, read: seqReads(home)}); err != nil || !strings.Contains(out.String(), `"state":"online"`) {
		t.Fatalf("until now: %v %s", err, out.String())
	}

	// --until offline via polling, ignoring a Wi-Fi change on the way.
	c, out = pageCtx()
	err = watchNetworkLoop(c, netWatch{until: "offline", interval: time.Millisecond, read: seqReads(home, cafe, netState{})})
	if err != nil || strings.Count(out.String(), "\n") != 2 {
		t.Fatalf("until offline: %v\n%s", err, out.String())
	}

	// --timeout with --until exits 3.
	c, _ = pageCtx()
	err = watchNetworkLoop(c, netWatch{until: "offline", interval: time.Millisecond, timeout: 20 * time.Millisecond, read: seqReads(home)})
	if exitCode(err) != exitNotFound {
		t.Fatalf("timeout: %v", err)
	}
}

func TestWatchNetworkFlags(t *testing.T) {
	c, _ := pageCtx()
	for _, args := range [][]string{{"--until=maybe"}, {"--interval=100ms"}, {"--timeout=x"}} {
		if err := runWatchNetwork(c, args); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
}
