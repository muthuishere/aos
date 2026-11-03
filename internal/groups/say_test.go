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

func TestNarrationClipsUseVideoTime(t *testing.T) {
	doc := videoDoc{Offset: 0.5, Duration: 10, Events: []videoEvent{
		{T: 1.5, Kind: "say", Label: "/a.aiff"},
		{T: 0.2, Kind: "say", Label: "/b.aiff"}, // before the first frame: clamp to 0
		{T: 3, Kind: "click"},
		{T: 12, Kind: "say", Label: "/late.aiff"}, // after the end: dropped
	}}
	got := narrationClips(doc)
	if len(got) != 2 || got[0].At != 1.0 || got[1].At != 0 {
		t.Errorf("got %+v", got)
	}
}

func TestNarrationMixArgs(t *testing.T) {
	clips := []narrationClip{{Path: "a.aiff", At: 1.25}, {Path: "b.aiff", At: 4}}
	mix := strings.Join(narrationMixArgs("v.mp4", clips, true, 9.5, "o.mp4"), " ")
	for _, want := range []string{"-i a.aiff", "-i b.aiff", "adelay=1250|1250", "adelay=4000|4000", "[0:a][n0][n1]amix=inputs=3", "-c:v copy", "-t 9.500"} {
		if !strings.Contains(mix, want) {
			t.Errorf("missing %q in %s", want, mix)
		}
	}
	replace := strings.Join(narrationMixArgs("v.mp4", clips, false, 0, "o.mp4"), " ")
	if strings.Contains(replace, "[0:a]") || !strings.Contains(replace, "amix=inputs=2") || strings.Contains(replace, " -t ") {
		t.Errorf("replace mode: %s", replace)
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
