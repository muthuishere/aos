package engine

import (
	"strings"
	"testing"
)

const rig = `<svg viewBox="0 0 10 10"><g id="shirt-base" fill="#808080"><path d="M0 0h1v1z"/></g><g id="shirt-shadow" fill="#808080"><path d="M0 0h1v1z"/></g><g id="eyes-open"><path d="M0 0h1v1z"/></g><g id="eyes-closed" opacity="0"><g><path d="M0 0h1v1z"/></g></g><g id="mouth-open" opacity="0"><path d="M0 0h1v1z"/></g></svg>`

func TestApplySVGPartsDropsHiddenFrames(t *testing.T) {
	out, err := ApplySVGParts([]byte(rig), SVGParts{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "eyes-closed") || strings.Contains(s, "mouth-open") || !strings.Contains(s, "eyes-open") || !strings.HasSuffix(s, "</svg>") {
		t.Fatalf("hidden frames not cut cleanly: %s", s)
	}
}

func TestApplySVGPartsBlinkAndRecolor(t *testing.T) {
	out, err := ApplySVGParts([]byte(rig), SVGParts{Show: []string{"eyes-closed"}, Hide: []string{"eyes-open"}, Recolor: map[string]string{"shirt": "#10b981"}})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if strings.Contains(s, "eyes-open") || !strings.Contains(s, `<g id="eyes-closed"><g>`) {
		t.Fatalf("blink frame wrong: %s", s)
	}
	if !strings.Contains(s, `id="shirt-base" fill="#10b981"`) || !strings.Contains(s, `id="shirt-shadow" fill="#009d65"`) {
		t.Fatalf("recolor wrong: %s", s)
	}
	if _, err := rasterizeSVG(out, 20, 20); err != nil {
		t.Fatal(err)
	}
}

func TestApplySVGPartsUnknownNames(t *testing.T) {
	if _, err := ApplySVGParts([]byte(rig), SVGParts{Show: []string{"tail"}}); err == nil || !strings.Contains(err.Error(), "eyes-closed") {
		t.Fatalf("want error listing groups, got %v", err)
	}
	if _, err := ApplySVGParts([]byte(rig), SVGParts{Recolor: map[string]string{"hat": "#000000"}}); err == nil {
		t.Fatal("want error for missing part")
	}
}
