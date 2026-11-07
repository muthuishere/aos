package groups

import (
	"strings"
	"testing"
	"time"
)

const focusFixture = `{"data":[{
 "storeAssertionRecords":[
  {"assertionUUID":"A","assertionDetails":{"assertionDetailsModeIdentifier":"com.apple.donotdisturb.mode.default"}},
  {"assertionUUID":"B","assertionDetails":{"assertionDetailsModeIdentifier":"com.apple.focus.work",
    "assertionDetailsLifetime":{"assertionDetailsLifetimeType":"date-interval","assertionDetailsDateIntervalLifetimeEndDateTimestamp":804616322}}}
 ],
 "storeInvalidationRecords":[{"invalidationAssertion":{"assertionUUID":"%s"}}]
}]}`

func TestParseFocusAssertions(t *testing.T) {
	now := appleEpoch.Add(804612722 * time.Second)

	// A is active and open-ended.
	st, err := parseFocusAssertions([]byte(strings.Replace(focusFixture, "%s", "Z", 1)), now)
	if err != nil || !st.On || st.Mode != "do-not-disturb" || st.Until != "" {
		t.Errorf("open-ended: %+v %v", st, err)
	}
	// A was ended: B (work, ends in an hour) is the live one.
	st, _ = parseFocusAssertions([]byte(strings.Replace(focusFixture, "%s", "A", 1)), now)
	if !st.On || st.Mode != "work" || st.Until == "" {
		t.Errorf("timed: %+v", st)
	}
	// Past B's end: off.
	st, _ = parseFocusAssertions([]byte(strings.Replace(focusFixture, "%s", "A", 1)), now.Add(2*time.Hour))
	if st.On {
		t.Errorf("expired assertion read as on: %+v", st)
	}
	if st, _ := parseFocusAssertions([]byte(`{"data":[]}`), now); st.On {
		t.Error("empty store should be off")
	}
}

func TestFocusModeName(t *testing.T) {
	for in, want := range map[string]string{"com.apple.donotdisturb.mode.default": "do-not-disturb", "com.apple.focus.work": "work", "": "do-not-disturb"} {
		if got := focusModeName(in); got != want {
			t.Errorf("focusModeName(%q) = %q", in, got)
		}
	}
}

func TestParseFocusFor(t *testing.T) {
	if d, err := parseFocusFor("1h"); err != nil || d != time.Hour {
		t.Errorf("1h: %v %v", d, err)
	}
	for _, bad := range []string{"10s", "an hour"} {
		if _, err := parseFocusFor(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
}

func TestGnomeDND(t *testing.T) {
	if gnomeBanners(true) != "false" || gnomeBanners(false) != "true" {
		t.Error("banners are the inverse of DND")
	}
	if got := focusRestoreScript(time.Hour); got != "sleep 3600; gsettings set org.gnome.desktop.notifications show-banners true" {
		t.Errorf("got %q", got)
	}
	if got := describeFocus(focusState{On: true, Mode: "work", Until: "T"}); got != "on  work  until T" {
		t.Errorf("got %q", got)
	}
}
