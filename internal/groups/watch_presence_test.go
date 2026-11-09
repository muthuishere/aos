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

func TestConsentApps(t *testing.T) {
	entries := []consentEntry{
		{Key: "Microsoft.WindowsCamera_8wekyb3d8bbwe", Start: 200, Stop: 0},                    // holding it
		{Key: `C:#Program Files#Zoom#bin#Zoom.exe`, NonPackaged: true, Start: 300, Stop: 100}, // stop older than start
		{Key: `C:#Tools#obs64.exe`, NonPackaged: true, Start: 100, Stop: 150},                  // released
		{Key: "Never.Used_abc", Start: 0, Stop: 0},                                             // no use recorded
	}
	got := consentApps(entries)
	want := []string{"Microsoft.WindowsCamera", "Zoom.exe"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consentApps = %v, want %v", got, want)
	}
}

// fakeProbe replays a script of samples, then repeats the last one.
func fakeProbe(samples ...any) func() (deviceUse, error) {
	i := 0
	return func() (deviceUse, error) {
		s := samples[min(i, len(samples)-1)]
		i++
		if err, ok := s.(error); ok {
			return deviceUse{}, err
		}
		return s.(deviceUse), nil
	}
}

func runPresence(o presenceOpts, probe func() (deviceUse, error)) (string, error) {
	var out, errb bytes.Buffer
	c := &cli.Ctx{Stdout: &out, Stderr: &errb}
	if o.Interval == 0 {
		o.Interval = time.Millisecond
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Unix(0, 0).UTC() }
	}
	err := watchPresence(context.Background(), c, o, probe)
	return out.String(), err
}

func TestWatchPresenceUntilFreeWaitsForTransition(t *testing.T) {
	busy := deviceUse{Busy: true, Apps: []string{"Zoom.exe"}}
	free := deviceUse{}
	out, err := runPresence(presenceOpts{Kind: "camera", Until: "free"}, fakeProbe(busy, busy, free))
	if err != nil {
		t.Fatal(err)
	}
	if out != "camera free\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestWatchPresenceUntilAlreadyMetReturnsAtOnce(t *testing.T) {
	out, err := runPresence(presenceOpts{Kind: "mic", Until: "busy"}, fakeProbe(deviceUse{Busy: true, Apps: []string{"us.zoom.xos"}}))
	if err != nil || out != "mic busy (us.zoom.xos)\n" {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestWatchPresenceTimeoutIsExit3(t *testing.T) {
	_, err := runPresence(presenceOpts{Kind: "camera", Until: "busy", Timeout: 20 * time.Millisecond}, fakeProbe(deviceUse{}))
	var exit *cli.ExitError
	if !errors.As(err, &exit) || exit.Code != 3 {
		t.Fatalf("err = %v, want exit 3", err)
	}
}

func TestWatchPresenceStreamPrintsBaselineAndChanges(t *testing.T) {
	a := deviceUse{Busy: true, Apps: []string{"a"}}
	b := deviceUse{Busy: true, Apps: []string{"a", "b"}} // same state, new app: reported
	samples := []any{deviceUse{}, deviceUse{}, a, a, b, deviceUse{}}
	var out strings.Builder
	var errb bytes.Buffer
	c := &cli.Ctx{Stdout: &out, Stderr: &errb}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	probe := fakeProbe(samples...)
	err := watchPresence(ctx, c, presenceOpts{Kind: "camera", Interval: time.Millisecond, Timeout: 100 * time.Millisecond, Now: time.Now}, probe)
	if err != nil {
		t.Fatalf("stream timeout should end cleanly, got %v", err)
	}
	want := "camera free\ncamera busy (a)\ncamera busy (a, b)\ncamera free\n"
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestWatchPresenceJSON(t *testing.T) {
	out, err := runPresence(presenceOpts{Kind: "mic", Until: "busy", JSON: true}, fakeProbe(deviceUse{Busy: true, Apps: []string{"x"}}))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"event":"mic","state":"busy","apps":["x"],"at":"1970-01-01T00:00:00Z","seq":1}` + "\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestWatchPresenceFirstProbeErrorFails(t *testing.T) {
	_, err := runPresence(presenceOpts{Kind: "camera"}, fakeProbe(errors.New("boom")))
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchPresenceBadFlags(t *testing.T) {
	c := &cli.Ctx{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	for _, args := range [][]string{{"--until=maybe"}, {"--interval=1ms"}, {"--timeout=soon"}, {"--bogus"}, {"extra"}} {
		if err := runWatchPresence(c, "camera", fakeProbe(deviceUse{}), args); err == nil {
			t.Errorf("args %v accepted", args)
		}
	}
}
