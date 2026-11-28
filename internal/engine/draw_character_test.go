package engine

import (
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestEveryRoleAndGenderBuildsARiggedCharacter(t *testing.T) {
	for _, role := range CharacterRoles {
		for _, gender := range []string{"man", "woman"} {
			svg, err := BuildCharacter(CharacterSpec{Role: role, Gender: gender, Beard: true, Glasses: true})
			if err != nil {
				t.Fatalf("%s %s: %v", role, gender, err)
			}
			if err := checkSVG(svg); err != nil {
				t.Fatalf("%s %s uses something aos cannot draw: %v", role, gender, err)
			}
			for _, frame := range []SVGParts{{}, {Show: []string{"eyes-closed"}, Hide: []string{"eyes-open"}}, {Show: []string{"mouth-open"}, Hide: []string{"mouth-closed"}}} {
				data, err := ApplySVGParts(svg, frame)
				if err != nil {
					t.Fatalf("%s %s frame %v: %v", role, gender, frame, err)
				}
				if _, err := rasterizeSVG(data, 80, 200); err != nil {
					t.Fatalf("%s %s: %v", role, gender, err)
				}
			}
			for _, id := range []string{"head-base", "arm-left", "arm-right", "data-speech", "data-floor"} {
				if !strings.Contains(string(svg), id) {
					t.Errorf("%s %s lacks %s", role, gender, id)
				}
			}
		}
	}
}

func TestCharacterPresetsAndAliases(t *testing.T) {
	for _, n := range CharacterPresetNames() {
		if _, err := BuildCharacter(CharacterPresets[n]); err != nil {
			t.Errorf("preset %s: %v", n, err)
		}
	}
	a, _ := BuildCharacter(CharacterSpec{Role: "programmer"})
	b, _ := BuildCharacter(CharacterSpec{Role: "developer"})
	if string(a) != string(b) {
		t.Error("programmer should be the developer template")
	}
	if len(CharacterPresets) < 6 {
		t.Errorf("only %d presets", len(CharacterPresets))
	}
}

func TestCharacterLooksAndErrors(t *testing.T) {
	svg, err := BuildCharacter(CharacterSpec{Skin: "#123456", Hair: "blonde", Outfit: "abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"#123456", hairTones["blonde"], "#abcdef"} {
		if !strings.Contains(string(svg), want) {
			t.Errorf("missing colour %s", want)
		}
	}
	for _, bad := range []CharacterSpec{{Role: "wizard"}, {Gender: "robot"}, {HairStyle: "mohawk"}, {Skin: "green"}} {
		if _, err := BuildCharacter(bad); err == nil {
			t.Errorf("%+v should fail", bad)
		}
	}
}

// CHAR_GALLERY=<dir> writes one PNG per preset (used for the docs gallery).
func TestCharacterGallery(t *testing.T) {
	dir := os.Getenv("CHAR_GALLERY")
	if dir == "" {
		t.Skip("set CHAR_GALLERY to write the preset PNGs")
	}
	for _, n := range CharacterPresetNames() {
		svg, _ := BuildCharacter(CharacterPresets[n])
		img, err := rasterizeSVG(svg, 160, 400)
		if err != nil {
			t.Fatal(err)
		}
		f, _ := os.Create(dir + "/" + n + ".png")
		png.Encode(f, img)
		f.Close()
	}
}
