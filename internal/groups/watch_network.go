package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "watch", Name: "network",
			Summary:  "Print a JSON line when the machine goes online or offline or changes Wi-Fi network",
			Blocking: true,
			Args:     "[--until=online|offline] [--timeout=<duration>] [--max=<n>] [--interval=<5s>]",
			Examples: []string{
				"aos watch network",
				"aos watch network --until=online --timeout=10m && aos exec -- git push",
				"aos watch network --until=offline",
				"# --until: exit 0 when that state is reached (at once if already there), 3 on --timeout",
			},
			Run: runWatchNetwork,
		})
	})
}

const (
	defaultNetPoll = 5 * time.Second
	minNetPoll     = time.Second
	// netSettle lets an interface finish coming up (address, route, SSID)
	// before it is read after an OS notification.
	netSettle = 700 * time.Millisecond
)

// netEvent is the watch envelope (event, at, seq) for connectivity.
type netEvent struct {
	Event     string `json:"event"`
	At        string `json:"at"`
	Seq       int    `json:"seq"`
	Change    string `json:"change"` // online | offline | wifi
	State     string `json:"state"`  // online | offline
	Interface string `json:"interface,omitempty"`
	IP        string `json:"ip,omitempty"`
	SSID      string `json:"ssid,omitempty"`
	PrevSSID  string `json:"prev_ssid,omitempty"`
}

func netOnline(s netState) bool { return s.Interface != "" && s.IP != "" }

func netStateName(s netState) string {
	if netOnline(s) {
		return "online"
	}
	return "offline"
}

// netChange names what changed between two snapshots: online/offline when
// connectivity flips, wifi when only the joined network does, "" otherwise.
// A new address on the same network is not news.
func netChange(prev, cur netState) string {
	if netOnline(prev) != netOnline(cur) {
		return netStateName(cur)
	}
	if netOnline(cur) && prev.SSID != cur.SSID {
		return "wifi"
	}
	return ""
}

type netWatch struct {
	until    string
	timeout  time.Duration
	max      int
	interval time.Duration
	read     func() (netState, error)
	notify   <-chan struct{} // OS change notifications; nil polls only
	now      func() time.Time
	settle   time.Duration
}

// Seams for tests.
var (
	netRead     = readNetwork
	netNotifier = startNetNotifier
)

func runWatchNetwork(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "until", "timeout", "max", "interval")
	if err != nil {
		return err
	}
	if err := set.Reject("until", "timeout", "max", "interval"); err != nil {
		return err
	}
	until := set.String("until", "")
	if until != "" && until != "online" && until != "offline" {
		return fmt.Errorf("--until must be online or offline, got %q", until)
	}
	interval, err := parseInterval(set.String("interval", ""), defaultNetPoll)
	if err != nil {
		return err
	}
	if interval < minNetPoll {
		return fmt.Errorf("--interval for watch network must be at least %s", minNetPoll)
	}
	var timeout time.Duration
	if v := set.String("timeout", ""); v != "" {
		if timeout, err = time.ParseDuration(v); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	max, err := set.Int("max", 0)
	if err != nil || max < 0 {
		return fmt.Errorf("--max must be a number of at least 0")
	}
	notify, stopNotify, err := netNotifier()
	if err != nil {
		// Polling still sees every change, a little later.
		c.Warnf("aos: no network change notifications (%v); polling every %s\n", err, interval)
		notify = nil
	}
	defer stopNotify()
	return watchNetworkLoop(c, netWatch{until: until, timeout: timeout, max: max, interval: interval, read: netRead, notify: notify, settle: netSettle})
}

// watchNetworkLoop reads the network on every OS notification and on a slow
// poll (the fallback, and what catches an SSID change the OS did not signal),
// and prints a line per change. The first read is the baseline; with --until
// it is also checked, so waiting for a state the machine is already in
// returns at once.
func watchNetworkLoop(c *cli.Ctx, o netWatch) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.now == nil {
		o.now = time.Now
	}
	var deadline <-chan time.Time
	if o.timeout > 0 {
		t := time.NewTimer(o.timeout)
		defer t.Stop()
		deadline = t.C
	}
	ticker := time.NewTicker(o.interval)
	defer ticker.Stop()

	emit := func(seq int, change string, prev, cur netState) error {
		e := netEvent{Event: "network", At: o.now().Format(time.RFC3339), Seq: seq, Change: change,
			State: netStateName(cur), Interface: cur.Interface, IP: cur.IP, SSID: cur.SSID}
		if change == "wifi" {
			e.PrevSSID = prev.SSID
		}
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		c.Printf("%s\n", line)
		return nil
	}

	prev, err := o.read()
	if err != nil {
		return err
	}
	seq := 0
	if o.until != "" && netStateName(prev) == o.until {
		return emit(1, o.until, prev, prev)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-deadline:
			if o.until != "" {
				return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("still %s after %s", netStateName(prev), o.timeout)}
			}
			return nil
		case <-ticker.C:
		case <-o.notify:
			time.Sleep(o.settle)
		}
		cur, err := o.read()
		if err != nil {
			c.Warnf("aos: %v\n", err)
			continue
		}
		change := netChange(prev, cur)
		if change == "" {
			prev = cur
			continue
		}
		seq++
		if err := emit(seq, change, prev, cur); err != nil {
			return err
		}
		prev = cur
		if o.until != "" && netStateName(cur) == o.until {
			return nil
		}
		if o.until == "" && o.max > 0 && seq >= o.max {
			return nil
		}
	}
}
