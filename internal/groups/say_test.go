package groups

import (
	"strings"
	"testing"
)

func TestParseSay(t *testing.T) {
	o, err := parseSay([]string{"hello", "world", "--voice=Samantha", "--rate=200", "--out=x.mp3", "--async"})
	if err != nil {
		t.Fatal(err)
	}
	if o.req.Text != "hello world" || o.req.Voice != "Samantha" || o.req.Rate != 200 || !strings.HasSuffix(o.out, "x.mp3") || !o.async {
		t.Errorf("got %+v", o)
	}
	for _, bad := range [][]string{{}, {"hi", "--rate=10"}, {"hi", "--out=x.ogg"}, {"hi", "--loud"}} {
		if _, err := parseSay(bad); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}

func TestSplitDashDash(t *testing.T) {
	s, c, ok := splitDashDash([]string{"hi", "--voice=X", "--", "aos", "key", "type", "--", "x"})
	if !ok || strings.Join(s, " ") != "hi --voice=X" || strings.Join(c, " ") != "aos key type -- x" {
		t.Errorf("got %v %v %v", s, c, ok)
	}
	if _, _, ok := splitDashDash([]string{"hi"}); ok {
		t.Error("no -- should not split")
	}
}

func TestParseMacVoices(t *testing.T) {
	out := "Albert              en_US    # Hello! My name is Albert.\nEddy (English (US)) en_US    # Hi\nbad line\n"
	v := parseMacVoices(out)
	if len(v) != 2 || v[0].Name != "Albert" || v[0].Language != "en-US" || v[1].Name != "Eddy (English (US))" {
		t.Errorf("got %+v", v)
	}
}

func TestConvertArgs(t *testing.T) {
	if a := strings.Join(convertArgs("in.aiff", "o.mp3"), " "); !strings.Contains(a, "libmp3lame") {
		t.Error(a)
	}
}
