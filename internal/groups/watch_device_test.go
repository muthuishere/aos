package groups

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func dev(kind, name string) watchedDevice {
	return watchedDevice{Kind: kind, Name: name, key: kind + "\x00" + name}
}

func snap(devs ...watchedDevice) map[string]watchedDevice {
	m := map[string]watchedDevice{}
	for _, d := range devs {
		m[d.key] = d
	}
	return m
}

func TestDiffDevices(t *testing.T) {
	prev := snap(dev("volume", "USB1"), dev("audio", "Speakers"))
	cur := snap(dev("audio", "Speakers"), dev("bluetooth", "AirPods"), dev("volume", "USB2"))
	got := diffDevices(prev, cur)
	want := []deviceChange{
		{"removed", dev("volume", "USB1")},
		{"added", dev("bluetooth", "AirPods")},
		{"added", dev("volume", "USB2")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diff = %+v\nwant %+v", got, want)
	}
	if len(diffDevices(cur, cur)) != 0 {
		t.Fatal("identical snapshots must not differ")
	}
}

func TestParseDeviceKinds(t *testing.T) {
	if k, err := parseDeviceKinds(""); err != nil || len(k) != 3 {
		t.Fatalf("default = %v %v", k, err)
	}
	if k, err := parseDeviceKinds("audio, volume,audio"); err != nil || !reflect.DeepEqual(k, []string{"audio", "volume"}) {
		t.Fatalf("got %v %v", k, err)
	}
	if _, err := parseDeviceKinds("usb"); err == nil {
		t.Fatal("unknown kind accepted")
	}
}

// scripted returns a lister that replays snapshots, then repeats the last.
func scripted(steps ...any) kindLister {
	i := 0
	return func() ([]watchedDevice, error) {
		s := steps[min(i, len(steps)-1)]
		i++
		if err, ok := s.(error); ok {
			return nil, err
		}
		return s.([]watchedDevice), nil
	}
}

func runDevices(o deviceWatchOpts, listers map[string]kindLister) (string, string, error) {
	var out, errb bytes.Buffer
	c := &cli.Ctx{Stdout: &out, Stderr: &errb}
	if o.Interval == 0 {
		o.Interval = time.Millisecond
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Unix(0, 0).UTC() }
	}
	err := watchDevices(context.Background(), c, o, listers)
	return out.String(), errb.String(), err
}

func TestWatchDevicesUntilAddedIgnoresBaselineAndRemovals(t *testing.T) {
	l := scripted(
		[]watchedDevice{dev("volume", "Old")},   // baseline: not news
		[]watchedDevice{},                       // Old removed: not what we wait for
		[]watchedDevice{dev("volume", "Stick")}, // added: done
	)
	out, _, err := runDevices(deviceWatchOpts{Kinds: []string{"volume"}, Until: "added"}, map[string]kindLister{"volume": l})
	if err != nil {
		t.Fatal(err)
	}
	if out != "added volume: Stick\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestWatchDevicesJSONAndAudioDetail(t *testing.T) {
	a := watchedDevice{Kind: "audio", Name: "Headset", Detail: "output", key: "audio\x00h\x00output"}
	l := scripted([]watchedDevice{}, []watchedDevice{a})
	out, _, err := runDevices(deviceWatchOpts{Kinds: []string{"audio"}, Until: "added", JSON: true}, map[string]kindLister{"audio": l})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"event":"device","change":"added","kind":"audio","name":"Headset","detail":"output","at":"1970-01-01T00:00:00Z","seq":1}` + "\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestWatchDevicesTimeoutExit3(t *testing.T) {
	l := scripted([]watchedDevice{})
	_, _, err := runDevices(deviceWatchOpts{Kinds: []string{"volume"}, Until: "removed", Timeout: 20 * time.Millisecond}, map[string]kindLister{"volume": l})
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 3 {
		t.Fatalf("err = %v, want exit 3", err)
	}
}

func TestWatchDevicesFlakyListingIsNotARemoval(t *testing.T) {
	l := scripted(
		[]watchedDevice{dev("bluetooth", "Mouse")},
		errors.New("radio busy"),
		[]watchedDevice{dev("bluetooth", "Mouse")},
		[]watchedDevice{},
	)
	out, errs, err := runDevices(deviceWatchOpts{Kinds: []string{"bluetooth"}, Until: "removed"}, map[string]kindLister{"bluetooth": l})
	if err != nil {
		t.Fatal(err)
	}
	if out != "removed bluetooth: Mouse\n" || !strings.Contains(errs, "radio busy") {
		t.Fatalf("out=%q err=%q", out, errs)
	}
}

func TestWatchDevicesUnlistableKind(t *testing.T) {
	bad := scripted(errors.New("no bluetooth"))
	good := scripted([]watchedDevice{dev("volume", "A")}, []watchedDevice{})
	listers := map[string]kindLister{"bluetooth": bad, "volume": good}

	// Default kinds: the broken one is skipped with a warning.
	out, errs, err := runDevices(deviceWatchOpts{Kinds: []string{"volume", "bluetooth"}, Until: "removed"}, listers)
	if err != nil || out != "removed volume: A\n" || !strings.Contains(errs, "not watching bluetooth") {
		t.Fatalf("out=%q errs=%q err=%v", out, errs, err)
	}
	// Asked for by name: that is an error.
	_, _, err = runDevices(deviceWatchOpts{Kinds: []string{"bluetooth"}, Explicit: true}, map[string]kindLister{"bluetooth": scripted(errors.New("no bluetooth"))})
	if err == nil {
		t.Fatal("explicit unlistable kind must fail")
	}
}

func TestWatchDevicesStreamEndsOnTimeoutWithZero(t *testing.T) {
	l := scripted([]watchedDevice{}, []watchedDevice{dev("volume", "X")}, []watchedDevice{})
	out, _, err := runDevices(deviceWatchOpts{Kinds: []string{"volume"}, Timeout: 60 * time.Millisecond}, map[string]kindLister{"volume": l})
	if err != nil {
		t.Fatal(err)
	}
	if out != "added volume: X\nremoved volume: X\n" {
		t.Fatalf("out = %q", out)
	}
}

func TestWatchDeviceBadFlags(t *testing.T) {
	c := &cli.Ctx{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	for _, args := range [][]string{{"--kind=usb"}, {"--until=gone"}, {"--interval=10ms"}, {"--timeout=soon"}, {"--bogus"}, {"x"}} {
		if err := runWatchDevice(c, args); err == nil {
			t.Errorf("args %v accepted", args)
		}
	}
}
