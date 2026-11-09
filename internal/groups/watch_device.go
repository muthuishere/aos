package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// watch device reports devices that appear or disappear: a USB drive mounted,
// a headset plugged in, a Bluetooth device connecting. It polls the same
// listings `audio devices` and `bluetooth list` print and diffs them, so it
// needs no driver and no OS event API, and every OS that can list a kind can
// watch it.
//
//	aos watch device --kind=volume --until=added && <the agent copies the files>

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "watch", Name: "device",
			Summary:  "Report when a USB drive, audio device or Bluetooth device is connected or removed",
			Blocking: true,
			Args:     "[--kind=volume|audio|bluetooth[,...]] [--until=added|removed] [--interval=<2s>] [--timeout=<duration>] [--json]",
			Examples: []string{
				"aos watch device",
				"aos watch device --kind=volume --until=added --timeout=2m",
				"aos watch device --kind=audio,bluetooth --json",
				"aos watch device --kind=bluetooth --until=removed && echo 'headphones dropped, pause the music'",
				"# with --until: exit 0 on the first matching event, 3 on --timeout",
			},
			Run: runWatchDevice,
		})
	})
}

// watchedDevice is one thing that is currently connected. Key identifies it
// across polls; Name is what a person calls it.
type watchedDevice struct {
	Kind   string `json:"kind"` // volume | audio | bluetooth
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"` // audio: output|input; volume: mount point
	key    string
}

// deviceChange is one device appearing or disappearing between two polls.
type deviceChange struct {
	Change string // added | removed
	Device watchedDevice
}

// deviceEvent is one line of --json output.
type deviceEvent struct {
	Event  string `json:"event"`  // always "device"
	Change string `json:"change"` // added | removed
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Detail string `json:"detail,omitempty"`
	At     string `json:"at"`
	Seq    int    `json:"seq"`
}

var deviceKinds = []string{"volume", "audio", "bluetooth"}

// diffDevices reports what was added and removed between two snapshots keyed
// by device identity, in a stable order (removals first, then by kind, name).
func diffDevices(prev, cur map[string]watchedDevice) []deviceChange {
	var out []deviceChange
	for k, d := range cur {
		if _, ok := prev[k]; !ok {
			out = append(out, deviceChange{"added", d})
		}
	}
	for k, d := range prev {
		if _, ok := cur[k]; !ok {
			out = append(out, deviceChange{"removed", d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Change != b.Change {
			return a.Change == "removed"
		}
		if a.Device.Kind != b.Device.Kind {
			return a.Device.Kind < b.Device.Kind
		}
		return a.Device.Name < b.Device.Name
	})
	return out
}

// parseDeviceKinds reads --kind: one or more of volume, audio, bluetooth.
func parseDeviceKinds(v string) ([]string, error) {
	if v == "" {
		return append([]string{}, deviceKinds...), nil
	}
	var out []string
	seen := map[string]bool{}
	for _, k := range strings.Split(v, ",") {
		k = strings.TrimSpace(k)
		valid := false
		for _, known := range deviceKinds {
			valid = valid || k == known
		}
		if !valid {
			return nil, fmt.Errorf("--kind must be volume, audio or bluetooth (comma separated), got %q", k)
		}
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out, nil
}

// kindLister lists what is connected for one kind.
type kindLister func() ([]watchedDevice, error)

// deviceListers is the real listings, reusing the code behind `audio devices`
// and `bluetooth list`. Only a connected Bluetooth device counts as present.
func deviceListers() map[string]kindLister {
	return map[string]kindLister{
		"volume": func() ([]watchedDevice, error) {
			vols, err := listVolumes() // the listing behind `disk list`
			var out []watchedDevice
			for _, v := range vols {
				name := v.Label
				if name == "" {
					name = filepath.Base(v.Mount)
				}
				if name == "" || name == "." || name == string(filepath.Separator) {
					name = v.Mount
				}
				out = append(out, watchedDevice{Kind: "volume", Name: name, Detail: v.Mount, key: "volume\x00" + v.Mount})
			}
			return out, err
		},
		"audio": func() ([]watchedDevice, error) {
			devs, err := listAudioDevices()
			var out []watchedDevice
			for _, d := range devs {
				out = append(out, watchedDevice{Kind: "audio", Name: d.Name, Detail: d.Direction, key: "audio\x00" + d.ID + "\x00" + d.Direction})
			}
			return out, err
		},
		"bluetooth": func() ([]watchedDevice, error) {
			devs, err := listBluetooth()
			var out []watchedDevice
			for _, d := range devs {
				if d.Connected {
					out = append(out, watchedDevice{Kind: "bluetooth", Name: d.Name, Detail: d.Type, key: "bluetooth\x00" + d.Address})
				}
			}
			return out, err
		},
	}
}

type deviceWatchOpts struct {
	Kinds    []string
	Explicit bool   // --kind was given: a kind that cannot be listed is an error, not skipped
	Until    string // "", added or removed
	Interval time.Duration
	Timeout  time.Duration
	JSON     bool
	Now      func() time.Time
}

func runWatchDevice(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "kind", "until", "interval", "timeout")
	if err != nil {
		return err
	}
	if err := set.Reject("kind", "until", "interval", "timeout", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`watch device` takes no positional arguments")
	}
	o := deviceWatchOpts{Until: set.String("until", ""), JSON: set.Has("json"), Explicit: set.String("kind", "") != "", Now: time.Now}
	if o.Kinds, err = parseDeviceKinds(set.String("kind", "")); err != nil {
		return err
	}
	if o.Until != "" && o.Until != "added" && o.Until != "removed" {
		return fmt.Errorf("--until must be added or removed, got %q", o.Until)
	}
	if o.Interval, err = parseInterval(set.String("interval", ""), 2*time.Second); err != nil {
		return err
	}
	if o.Interval < 500*time.Millisecond {
		return fmt.Errorf("--interval must be at least 500ms: listing Bluetooth is not free")
	}
	if v := set.String("timeout", ""); v != "" {
		if o.Timeout, err = parseInterval(v, 0); err != nil || o.Timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return watchDevices(ctx, c, o, deviceListers())
}

// snapshotKind lists one kind into a map by identity.
func snapshotKind(list kindLister) (map[string]watchedDevice, error) {
	devs, err := list()
	if err != nil {
		return nil, err
	}
	m := map[string]watchedDevice{}
	for _, d := range devs {
		if d.key == "" {
			d.key = d.Kind + "\x00" + d.Name
		}
		m[d.key] = d
	}
	return m, nil
}

// watchDevices polls every selected kind, prints each add/remove, and returns
// when --until is met. The first poll is the baseline (what is already
// connected is not news). A kind that fails after the baseline keeps its last
// snapshot, so a flaky listing never reads as every device being removed.
func watchDevices(ctx context.Context, c *cli.Ctx, o deviceWatchOpts, listers map[string]kindLister) error {
	if o.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.Timeout)
		defer cancel()
	}
	prev := map[string]map[string]watchedDevice{}
	active := []string{}
	for _, kind := range o.Kinds {
		snap, err := snapshotKind(listers[kind])
		if err != nil {
			if o.Explicit {
				return fmt.Errorf("cannot list %s devices: %w", kind, err)
			}
			c.Warnf("aos: not watching %s devices: %v\n", kind, err)
			continue
		}
		prev[kind] = snap
		active = append(active, kind)
	}
	if len(active) == 0 {
		return fmt.Errorf("no device kind could be listed on this machine")
	}

	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()
	seq := 0
	for {
		select {
		case <-ctx.Done():
			timedOut := ctx.Err() == context.DeadlineExceeded
			switch {
			case o.Until == "":
				return nil
			case timedOut:
				return &cli.ExitError{Code: 3, Message: fmt.Sprintf("timed out after %s waiting for a device to be %s", o.Timeout, o.Until)}
			default:
				return &cli.ExitError{Code: 130, Message: "interrupted"}
			}
		case <-ticker.C:
		}

		for _, kind := range active {
			cur, err := snapshotKind(listers[kind])
			if err != nil {
				c.Warnf("aos: %s: %v\n", kind, err)
				continue
			}
			for _, ch := range diffDevices(prev[kind], cur) {
				if o.Until != "" && ch.Change != o.Until {
					continue
				}
				seq++
				if err := printDeviceChange(c, o, ch, seq); err != nil {
					return err
				}
				if o.Until != "" {
					return nil
				}
			}
			prev[kind] = cur
		}
	}
}

func printDeviceChange(c *cli.Ctx, o deviceWatchOpts, ch deviceChange, seq int) error {
	d := ch.Device
	if o.JSON {
		line, err := json.Marshal(deviceEvent{
			Event: "device", Change: ch.Change, Kind: d.Kind, Name: d.Name, Detail: d.Detail,
			At: o.Now().Format(time.RFC3339), Seq: seq,
		})
		if err != nil {
			return err
		}
		c.Printf("%s\n", line)
		return nil
	}
	text := fmt.Sprintf("%s %s: %s", ch.Change, d.Kind, d.Name)
	if d.Kind == "audio" && d.Detail != "" {
		text += " (" + d.Detail + ")"
	}
	c.Println(text)
	return nil
}
