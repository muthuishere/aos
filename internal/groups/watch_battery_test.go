package groups

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func bat(pct int, onAC bool, status string) powerState {
	return powerState{HasBattery: true, Percent: pct, OnAC: onAC, Status: status}
}

func TestWbatMet(t *testing.T) {
	cases := []struct {
		name string
		s    powerState
		c    wbatConditions
		want string
	}{
		{"below holds", bat(15, false, "discharging"), wbatConditions{below: 20}, "below=20"},
		{"below is strict", bat(20, false, "discharging"), wbatConditions{below: 20}, ""},
		{"above holds", bat(81, true, "charging"), wbatConditions{above: 80}, "above=80"},
		{"above is strict", bat(80, true, "charging"), wbatConditions{above: 80}, ""},
		{"unknown level never matches", bat(-1, false, "discharging"), wbatConditions{below: 20}, ""},
		{"charging", bat(50, true, "charging"), wbatConditions{until: "charging"}, "until=charging"},
		{"finishing charge is charging", bat(99, true, "finishing charge"), wbatConditions{until: "charging"}, "until=charging"},
		{"on ac but not charging", bat(50, true, "AC attached"), wbatConditions{until: "charging"}, ""},
		{"discharging", bat(50, false, "discharging"), wbatConditions{until: "discharging"}, "until=discharging"},
		{"full by status (macOS)", bat(100, true, "charged"), wbatConditions{until: "full"}, "until=full"},
		{"full by 100% on mains (Windows)", bat(100, true, "not charging"), wbatConditions{until: "full"}, "until=full"},
		{"100% on battery is not full", bat(100, false, "discharging"), wbatConditions{until: "full"}, ""},
		{"any condition ends it", bat(50, false, "discharging"), wbatConditions{below: 20, until: "discharging"}, "until=discharging"},
	}
	for _, tc := range cases {
		got, ok := wbatMet(tc.s, tc.c)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("%s: got %q %v, want %q", tc.name, got, ok, tc.want)
		}
	}
}

func TestWbatChanges(t *testing.T) {
	cases := []struct {
		name      string
		prev, cur powerState
		want      []string
	}{
		{"nothing", bat(50, false, "discharging"), bat(50, false, "discharging"), nil},
		{"unplugged", bat(50, true, "charging"), bat(50, false, "discharging"), []string{"unplugged"}},
		{"plugged and level", bat(50, false, "discharging"), bat(51, true, "charging"), []string{"plugged", "level"}},
		{"reaches full", bat(99, true, "charging"), bat(100, true, "charged"), []string{"full", "level"}},
		{"already full stays quiet", bat(100, true, "charged"), bat(100, true, "charged"), nil},
		{"unknown level is not a change", bat(50, false, "discharging"), bat(-1, false, "discharging"), nil},
	}
	for _, tc := range cases {
		if got := wbatChanges(tc.prev, tc.cur); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

type wbatScript struct {
	reads  []powerState
	next   int
	clock  time.Time
	events []wbatEvent
}

func (s *wbatScript) watcher() *wbatWatcher {
	return &wbatWatcher{
		read: func() (powerState, error) {
			i := s.next
			if i >= len(s.reads) {
				i = len(s.reads) - 1
			}
			s.next++
			return s.reads[i], nil
		},
		now: func() time.Time { return s.clock },
		sleep: func(_ context.Context, d time.Duration) bool {
			s.clock = s.clock.Add(d)
			return true
		},
		emit: func(e wbatEvent) { s.events = append(s.events, e) },
	}
}

func TestWatchBatteryNoBattery(t *testing.T) {
	s := &wbatScript{reads: []powerState{{Percent: -1, OnAC: true}}}
	res, err := s.watcher().run(context.Background(), wbatConditions{below: 20}, time.Second, 0)
	if err != nil || res != wbatNoBattery {
		t.Fatalf("res %v err %v", res, err)
	}
}

func TestWatchBatteryBelowFiresOnTheCrossing(t *testing.T) {
	s := &wbatScript{reads: []powerState{bat(22, false, "discharging"), bat(21, false, "discharging"), bat(19, false, "discharging")}}
	res, _ := s.watcher().run(context.Background(), wbatConditions{below: 20}, time.Second, 0)
	if res != wbatMetCondition || len(s.events) != 1 || *s.events[0].Percent != 19 || s.events[0].Condition != "below=20" {
		t.Fatalf("res %v events %+v", res, s.events)
	}
	if s.clock.Sub(time.Time{}) != 2*time.Second {
		t.Fatalf("polled %v, want two intervals", s.clock.Sub(time.Time{}))
	}
}

func TestWatchBatteryAlreadyMetReturnsAtOnce(t *testing.T) {
	s := &wbatScript{reads: []powerState{bat(100, true, "charged")}}
	res, _ := s.watcher().run(context.Background(), wbatConditions{until: "full"}, time.Second, 0)
	if res != wbatMetCondition || !s.clock.IsZero() {
		t.Fatalf("res %v clock %v", res, s.clock)
	}
}

func TestWatchBatteryTimeout(t *testing.T) {
	s := &wbatScript{reads: []powerState{bat(50, false, "discharging")}}
	res, _ := s.watcher().run(context.Background(), wbatConditions{above: 90}, 5*time.Second, 12*time.Second)
	if res != wbatTimedOut || s.clock.Sub(time.Time{}) != 12*time.Second {
		t.Fatalf("res %v slept %v", res, s.clock.Sub(time.Time{}))
	}
}

func TestWatchBatteryStream(t *testing.T) {
	s := &wbatScript{reads: []powerState{
		bat(50, true, "charging"), bat(50, true, "charging"), bat(51, true, "charging"), bat(51, false, "discharging"),
	}}
	w := s.watcher()
	inner := w.sleep
	calls := 0
	w.sleep = func(ctx context.Context, d time.Duration) bool {
		calls++
		return calls <= 3 && inner(ctx, d)
	}
	res, _ := w.run(context.Background(), wbatConditions{}, time.Second, 0)
	if res != wbatInterrupted {
		t.Fatalf("res %v", res)
	}
	var got []string
	for _, e := range s.events {
		got = append(got, e.Event)
	}
	if !reflect.DeepEqual(got, []string{"level", "unplugged"}) {
		t.Fatalf("events %v", got)
	}
}

func TestWbatTimeout(t *testing.T) {
	for in, want := range map[string]time.Duration{"": 0, "30": 30 * time.Second, "3h": 3 * time.Hour} {
		if got, err := wbatTimeout(in); err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	if _, err := wbatTimeout("soon"); err == nil {
		t.Error("soon should fail")
	}
}
