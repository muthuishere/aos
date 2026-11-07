package groups

import "testing"

func TestNotifyScript(t *testing.T) {
	got := notifyScript(`Say "hi"`, `back\slash`, true)
	want := `display notification "back\\slash" with title "Say \"hi\"" sound name "default"`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
