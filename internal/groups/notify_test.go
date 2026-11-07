package groups

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNotifyText(t *testing.T) {
	title, body, err := notifyText([]string{" Build finished ", "all green"})
	if err != nil || title != "Build finished" || body != "all green" {
		t.Errorf("got %q %q %v", title, body, err)
	}
	if _, _, err := notifyText(nil); err == nil {
		t.Error("missing title should fail")
	}
	if _, _, err := notifyText([]string{"a", "b", "c"}); err == nil {
		t.Error("three words unquoted should fail")
	}
	long, _, _ := notifyText([]string{strings.Repeat("é", 100)})
	if utf8.RuneCountInString(long) != 63 || !strings.HasSuffix(long, "…") {
		t.Errorf("title not clipped to 63 runes: %d", utf8.RuneCountInString(long))
	}
}

func TestNotifySendArgs(t *testing.T) {
	got := strings.Join(notifySendArgs("-x", "body"), "|")
	if got != "--app-name=aos|--|-x|body" {
		t.Errorf("got %q", got)
	}
	if got := notifySendArgs("t", ""); len(got) != 3 {
		t.Errorf("empty body should be left out: %v", got)
	}
}
