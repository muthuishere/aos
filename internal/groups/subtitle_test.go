package groups

import (
	"testing"

	"github.com/muthuishere/aos/internal/engine"
)

func TestSubtitleOptionsKeepOldFlags(t *testing.T) {
	o, _, secs, err := subtitleOptions([]string{"hello", "world", "--seconds=4", "--position=top", "--size=60"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Text != "hello world" || secs != 4 || o.Position != "top" || o.FontSize != 60 || !o.Subtitle || o.Style != engine.DrawText {
		t.Errorf("got %+v secs=%v", o, secs)
	}
	if o, _, _, _ = subtitleOptions([]string{"hi", "--size=xl"}); o.Size != engine.SizeXL {
		t.Errorf("size xl: %+v", o.Size)
	}
	for _, bad := range [][]string{{"--seconds=0", "x"}, {"x", "--size=4"}, {""}} {
		if _, _, _, err := subtitleOptions(bad); err == nil {
			t.Errorf("%v should fail", bad)
		}
	}
}

func TestLegacySubtitleMapping(t *testing.T) {
	r := legacySubtitle(engine.DrawOptions{Text: "a\nb", Position: "top-left", Size: engine.SizeS}, 2.5)
	if r.Text != "a b" || r.Position != "top" || r.Seconds != 3 || r.Size != 24 {
		t.Errorf("got %+v", r)
	}
}
