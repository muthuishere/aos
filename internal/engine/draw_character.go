package engine

import (
	"fmt"
	"sort"
	"strings"
)

// Characters for explainer videos: one original flat style, composed from a
// role template (outfit) plus looks (gender, skin, hair, face). Every
// character is a rigged SVG: eyes-open / eyes-closed and mouth-closed /
// mouth-open frames (closed ones opacity 0, see SVGParts), arm-left /
// arm-right, colour groups named <part>-<tone>, and anchors on the root
// (data-speech, data-face, data-chest, data-floor, data-facing).

// CharacterSpec describes one character. Zero fields take the role's or
// gender's default.
type CharacterSpec struct {
	Role      string // see CharacterRoles (aliases: programmer, ceo, cook, ...)
	Gender    string // man, woman
	Skin      string // #hex or light, medium, tan, brown, dark
	Hair      string // #hex or black, brown, auburn, blonde, grey, white
	HairStyle string // short, side, curly, long, bun, ponytail, bald
	Outfit    string // #hex: the role's main colour
	Beard     bool
	Moustache bool
	Glasses   bool
}

// CharacterRoles are the outfit templates.
var CharacterRoles = []string{"casual", "customer", "developer", "founder", "office", "lawyer", "doctor", "nurse", "scientist", "chef",
	"shopkeeper", "teacher", "student", "police", "firefighter", "pilot", "mechanic", "builder", "farmer", "artist", "singer"}

// characterRoleAliases are other words for a role.
var characterRoleAliases = map[string]string{"programmer": "developer", "coder": "developer", "engineer": "developer",
	"ceo": "founder", "entrepreneur": "founder", "manager": "office", "construction": "builder", "worker": "builder",
	"shop": "shopkeeper", "seller": "shopkeeper", "cook": "chef", "officer": "police", "musician": "singer", "painter": "artist"}

// CharacterHairStyles are the hair shapes.
var CharacterHairStyles = []string{"short", "side", "curly", "long", "bun", "ponytail", "bald"}

// CharacterPresets is the ready cast; any field can still be overridden.
var CharacterPresets = map[string]CharacterSpec{
	"priya":   {Role: "shopkeeper", Gender: "woman", Skin: "medium", Hair: "black", HairStyle: "long"},
	"ramesh":  {Role: "shopkeeper", Gender: "man", Skin: "brown", Hair: "grey", HairStyle: "bald", Moustache: true},
	"dev":     {Role: "customer", Gender: "man", Skin: "light", Hair: "auburn", HairStyle: "curly"},
	"zoya":    {Role: "customer", Gender: "woman", Skin: "tan", Hair: "brown", HairStyle: "ponytail"},
	"arjun":   {Role: "developer", Gender: "man", Skin: "tan", Hair: "black", HairStyle: "short", Glasses: true},
	"nisha":   {Role: "developer", Gender: "woman", Skin: "medium", Hair: "black", HairStyle: "bun", Glasses: true},
	"meera":   {Role: "founder", Gender: "woman", Skin: "light", Hair: "brown", HairStyle: "bun"},
	"karan":   {Role: "founder", Gender: "man", Skin: "medium", Hair: "black", HairStyle: "side"},
	"tom":     {Role: "office", Gender: "man", Skin: "light", Hair: "blonde", HairStyle: "side"},
	"ravi":    {Role: "lawyer", Gender: "man", Skin: "brown", Hair: "black", HairStyle: "side", Moustache: true},
	"leela":   {Role: "lawyer", Gender: "woman", Skin: "medium", Hair: "grey", HairStyle: "bun", Glasses: true},
	"sana":    {Role: "doctor", Gender: "woman", Skin: "tan", Hair: "black", HairStyle: "ponytail"},
	"joseph":  {Role: "nurse", Gender: "man", Skin: "dark", Hair: "black", HairStyle: "short"},
	"ada":     {Role: "scientist", Gender: "woman", Skin: "light", Hair: "auburn", HairStyle: "long", Glasses: true},
	"kabir":   {Role: "chef", Gender: "man", Skin: "medium", Hair: "black", HairStyle: "short", Beard: true},
	"anita":   {Role: "teacher", Gender: "woman", Skin: "dark", Hair: "black", HairStyle: "curly", Glasses: true},
	"maya":    {Role: "student", Gender: "woman", Skin: "medium", Hair: "black", HairStyle: "ponytail"},
	"vikram":  {Role: "police", Gender: "man", Skin: "dark", Hair: "black", HairStyle: "short", Moustache: true},
	"rosa":    {Role: "firefighter", Gender: "woman", Skin: "tan", Hair: "brown", HairStyle: "bun"},
	"sam":     {Role: "pilot", Gender: "man", Skin: "brown", Hair: "black", HairStyle: "short"},
	"gopal":   {Role: "mechanic", Gender: "man", Skin: "brown", Hair: "black", HairStyle: "curly", Moustache: true},
	"lakshmi": {Role: "builder", Gender: "woman", Skin: "brown", Hair: "black", HairStyle: "long"},
	"murugan": {Role: "farmer", Gender: "man", Skin: "dark", Hair: "white", HairStyle: "short", Moustache: true},
	"isha":    {Role: "artist", Gender: "woman", Skin: "light", Hair: "black", HairStyle: "curly"},
	"leo":     {Role: "singer", Gender: "man", Skin: "tan", Hair: "brown", HairStyle: "curly", Beard: true},
}

// CharacterPresetNames lists the presets, sorted.
func CharacterPresetNames() []string {
	names := make([]string, 0, len(CharacterPresets))
	for n := range CharacterPresets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

var (
	skinTones = map[string]string{"light": "#f2c9a0", "medium": "#d9a066", "tan": "#c68642", "brown": "#a0662f", "dark": "#6e4220"}
	hairTones = map[string]string{"black": "#22201f", "brown": "#5a3825", "auburn": "#8a3b1c", "blonde": "#d9b25f", "grey": "#9a9a9a", "white": "#e8e6e1"}
	roleColor = map[string]string{"casual": "#3b82f6", "customer": "#e0679a", "developer": "#1f2a44", "founder": "#7a1f3d", "office": "#cfe0f2",
		"lawyer": "#16161a", "doctor": "#f4f6f8", "nurse": "#3aa38f", "scientist": "#f4f6f8", "chef": "#f7f7f5", "shopkeeper": "#2f7d4f",
		"teacher": "#b5651d", "student": "#f2b134", "police": "#2c3e66", "firefighter": "#2b2b2e", "pilot": "#1d2b4f", "mechanic": "#2f5f8f",
		"builder": "#f28c28", "farmer": "#3f6fa8", "artist": "#f2efe6", "singer": "#7b3fa0"}
)

func pickTone(v string, named map[string]string, fallback string) (string, error) {
	if v == "" {
		return fallback, nil
	}
	if c, ok := named[strings.ToLower(v)]; ok {
		return c, nil
	}
	if hexRe.MatchString(v) {
		if !strings.HasPrefix(v, "#") {
			v = "#" + v
		}
		return v, nil
	}
	keys := make([]string, 0, len(named))
	for k := range named {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "", fmt.Errorf("draw character: %q is not a colour: use #rrggbb or one of %s", v, strings.Join(keys, ", "))
}

func oneOf(v, what string, allowed []string) error {
	for _, a := range allowed {
		if v == a {
			return nil
		}
	}
	return fmt.Errorf("draw character: unknown %s %q: use one of %s", what, v, strings.Join(allowed, ", "))
}

// svgb builds the document.
type svgb struct{ strings.Builder }

func (b *svgb) g(id, fill string, body ...string) {
	fmt.Fprintf(b, "<g id=%q fill=%q>%s</g>", id, fill, strings.Join(body, ""))
}
func (b *svgb) raw(s string) { b.WriteString(s) }

func path(d string) string { return fmt.Sprintf("<path d=%q/>", d) }
func ell(cx, cy, rx, ry float64) string {
	return fmt.Sprintf("<ellipse cx=\"%g\" cy=\"%g\" rx=\"%g\" ry=\"%g\"/>", cx, cy, rx, ry)
}
func circ(cx, cy, r float64) string {
	return fmt.Sprintf("<circle cx=\"%g\" cy=\"%g\" r=\"%g\"/>", cx, cy, r)
}
func stroke(d, color string, w float64) string {
	return fmt.Sprintf("<path d=%q fill=\"none\" stroke=%q stroke-width=\"%g\" stroke-linecap=\"round\" stroke-linejoin=\"round\"/>", d, color, w)
}

// BuildCharacter renders spec as a rigged SVG (viewBox 0 0 400 1000).
func BuildCharacter(spec CharacterSpec) ([]byte, error) {
	spec.Role = strings.ToLower(spec.Role)
	if spec.Role == "" {
		spec.Role = "casual"
	}
	if real, ok := characterRoleAliases[spec.Role]; ok {
		spec.Role = real
	}
	if spec.Gender == "" {
		spec.Gender = "man"
	}
	if err := oneOf(spec.Role, "role", CharacterRoles); err != nil {
		return nil, err
	}
	if err := oneOf(spec.Gender, "gender", []string{"man", "woman"}); err != nil {
		return nil, err
	}
	woman := spec.Gender == "woman"
	if spec.HairStyle == "" {
		spec.HairStyle = map[bool]string{false: "short", true: "long"}[woman]
	}
	if err := oneOf(spec.HairStyle, "hair style", CharacterHairStyles); err != nil {
		return nil, err
	}
	skin, err := pickTone(spec.Skin, skinTones, skinTones["medium"])
	if err != nil {
		return nil, err
	}
	hair, err := pickTone(spec.Hair, hairTones, hairTones["black"])
	if err != nil {
		return nil, err
	}
	outfit, err := pickTone(spec.Outfit, map[string]string{}, roleColor[spec.Role])
	if err != nil {
		return nil, err
	}
	skinShadow := shadeHex(skin, -28)
	line := "#2a1d1a"

	// Body geometry: shoulders, waist and hips.
	sh, wa := 82.0, 70.0 // half widths at the shoulder line (y 312) and waist (y 560)
	if woman {
		sh, wa = 70, 60
	}
	cx := 200.0
	L, R := cx-sh, cx+sh

	var b svgb
	fmt.Fprintf(&b, "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 400 1000\" width=\"400\" height=\"1000\" "+
		"data-speech=\"262,240\" data-face=\"122,70,156,230\" data-chest=\"%g,330,%g,150\" data-floor=\"985\" data-facing=\"front\">", cx-60, 120.0)

	// Long hair falls behind the body.
	if spec.HairStyle == "long" {
		b.g("hair-base", hair, path("M124,190 C112,110 160,78 200,78 C240,78 288,110 276,190 L292,420 C260,440 140,440 108,420 Z"))
	}
	if spec.HairStyle == "ponytail" {
		b.g("hair-base", hair, path("M250,150 C300,170 310,260 290,330 C282,350 262,340 266,320 C280,260 270,200 240,175 Z"))
	}

	// Legs: trousers, shoes.
	coatRoles := map[string]bool{"founder": true, "lawyer": true, "doctor": true, "teacher": true, "scientist": true, "pilot": true, "singer": true}
	shortSleeve := map[string]bool{"casual": true, "customer": true, "shopkeeper": true, "nurse": true, "student": true, "builder": true}[spec.Role]
	trousers, ok := map[string]string{"casual": "#2b3441", "customer": "#3a4a6b", "developer": "#2b2f36", "founder": "#22252b", "office": "#2b2f3a",
		"lawyer": "#16161a", "doctor": "#2f6f8f", "nurse": outfit, "scientist": "#3b3f4a", "chef": "#2b2b2b", "shopkeeper": "#3a3a3a",
		"teacher": "#3b3f4a", "student": "#3a5a8c", "police": shadeHex(outfit, -20), "firefighter": outfit, "pilot": outfit,
		"mechanic": outfit, "builder": "#3a4a6b", "farmer": outfit, "artist": "#3b3f4a", "singer": "#1c1c22"}[spec.Role]
	if !ok {
		trousers = "#2b2f36"
	}
	b.g("shoes-base", "#1b1b1d", ell(160, 958, 34, 16), ell(240, 958, 34, 16))
	b.g("trousers-base", trousers, path(fmt.Sprintf("M%g,548 L%g,548 L%g,948 L208,948 L200,640 L192,948 L%g,948 Z", cx-wa-4, cx+wa+4, cx+wa-4, cx-wa+4)))

	// Torso: the shirt (or the inner shirt under a coat), then the role's layer.
	torso := fmt.Sprintf("M%g,330 Q%g,304 %g,300 L%g,300 Q%g,304 %g,330 L%g,566 L%g,566 Z", L, L+6, L+34, R-34, R-6, R, cx+wa, cx-wa)
	top, ok := map[string]string{"founder": "#f2f2f2", "lawyer": "#fafafa", "doctor": "#5aa6c8", "teacher": "#f3efe6", "scientist": "#8a96a6",
		"pilot": "#f7f7f7", "singer": "#1c1c22", "shopkeeper": "#e9e4d8", "mechanic": "#c9c2b3", "builder": "#7c8794", "farmer": "#b03a2e"}[spec.Role]
	if !ok {
		top = outfit
	}
	sleeve := outfit
	if s, ok := map[string]string{"shopkeeper": "#e9e4d8", "mechanic": "#c9c2b3", "builder": "#7c8794", "farmer": "#b03a2e"}[spec.Role]; ok {
		sleeve = s
	}
	b.g("shirt-base", top, path(torso))
	b.g("skin-base", skin, path(fmt.Sprintf("M%g,300 L%g,300 L200,338 Z", cx-26, cx+26)))
	tie := func(color string) {
		b.g("tie-base", color, path(fmt.Sprintf("M%g,312 L%g,312 L%g,330 L%g,420 L200,436 L%g,420 L%g,330 Z", cx-8, cx+8, cx+5, cx+9, cx-9, cx-5)))
	}
	if coatRoles[spec.Role] {
		b.g("shirt-shadow", shadeHex(top, -28), path(fmt.Sprintf("M%g,300 L%g,300 L200,326 Z", cx-22, cx+22)))
		if spec.Role == "pilot" {
			tie("#1b1b1d")
		}
		long := map[string]float64{"lawyer": 600, "doctor": 640, "scientist": 640}[spec.Role]
		if long == 0 {
			long = 566
		}
		panel := func(s float64) string {
			x0, xl := cx+s*28, cx+s*(sh+2)
			return path(fmt.Sprintf("M%g,300 L%g,300 Q%g,304 %g,330 L%g,%g L%g,%g L%g,430 Z", x0, cx+s*(sh-34), cx+s*(sh-6), xl, cx+s*(wa+6), long, cx+s*20, long, cx+s*12))
		}
		b.g("coat-base", outfit, panel(-1), panel(1))
		b.g("coat-shadow", shadeHex(outfit, -28), path(fmt.Sprintf("M%g,300 L%g,390 L%g,360 Z", cx-28, cx-12, cx-44)), path(fmt.Sprintf("M%g,300 L%g,390 L%g,360 Z", cx+28, cx+12, cx+44)))
		switch spec.Role {
		case "founder", "doctor", "scientist":
			b.g("button-base", "#d9d9d9", circ(cx+16, 470, 4), circ(cx+16, 510, 4))
		case "pilot":
			b.g("stripe-base", "#d4af37", path(fmt.Sprintf("M%g,318 h40 v8 h-40 Z M%g,318 h40 v8 h-40 Z", L+6, R-46)), circ(cx-40, 380, 6))
		case "lawyer":
			b.g("bands-base", "#ffffff", path(fmt.Sprintf("M%g,330 h9 v34 h-9 Z M%g,330 h9 v34 h-9 Z", cx-10, cx+1)))
		}
	}
	switch spec.Role {
	case "developer":
		b.g("shirt-shadow", shadeHex(outfit, -28), path(fmt.Sprintf("M%g,300 C%g,272 %g,272 %g,300 L%g,316 C%g,296 %g,296 %g,316 Z", cx-58, cx-40, cx+40, cx+58, cx+44, cx+30, cx-30, cx-44)),
			path(fmt.Sprintf("M%g,470 L%g,470 L%g,540 L%g,540 Z", cx-46, cx+46, cx+56, cx-56)))
		b.raw(stroke(fmt.Sprintf("M%g,322 L%g,380 M%g,322 L%g,380", cx-14, cx-16, cx+14, cx+16), "#e8e8e8", 3))
	case "office":
		b.g("shirt-shadow", shadeHex(outfit, -28), path(fmt.Sprintf("M%g,300 L%g,322 L%g,300 Z M%g,300 L%g,322 L%g,300 Z", cx-26, cx-10, cx-4, cx+26, cx+10, cx+4)))
		tie("#8b1e2d")
	case "chef":
		b.g("button-base", "#9a9a9a", circ(cx-18, 360, 5), circ(cx-18, 410, 5), circ(cx-18, 460, 5), circ(cx+18, 360, 5), circ(cx+18, 410, 5), circ(cx+18, 460, 5))
		b.g("scarf-base", "#c0392b", path(fmt.Sprintf("M%g,302 L%g,302 L200,330 Z", cx-30, cx+30)))
	case "shopkeeper":
		b.g("apron-base", outfit, path(fmt.Sprintf("M%g,368 L%g,368 L%g,640 L%g,640 Z", cx-42, cx+42, cx+60, cx-60)))
		b.raw(stroke(fmt.Sprintf("M%g,368 L%g,304 M%g,368 L%g,304", cx-40, cx-24, cx+40, cx+24), outfit, 8))
		b.g("apron-shadow", shadeHex(outfit, -28), path(fmt.Sprintf("M%g,470 L%g,470 L%g,520 L%g,520 Z", cx-30, cx+30, cx+30, cx-30)))
	case "police":
		b.g("badge-base", "#d4af37", path(fmt.Sprintf("M%g,380 l12,-6 l12,6 v16 l-12,8 l-12,-8 Z", cx+34)))
		b.g("shirt-shadow", shadeHex(outfit, -28), path(fmt.Sprintf("M%g,318 h40 v8 h-40 Z M%g,318 h40 v8 h-40 Z", L+6, R-46)))
		b.g("belt-base", "#1b1b1d", path(fmt.Sprintf("M%g,548 L%g,548 L%g,566 L%g,566 Z", cx-wa-2, cx+wa+2, cx+wa+2, cx-wa-2)))
	case "firefighter":
		b.g("stripe-base", "#f2d230", path(fmt.Sprintf("M%g,470 L%g,470 L%g,486 L%g,486 Z", cx-wa-2, cx+wa+2, cx+wa+2, cx-wa-2)), path(fmt.Sprintf("M%g,870 L%g,870 L%g,886 L%g,886 Z", cx-wa+2, cx+wa-2, cx+wa-2, cx-wa+2)))
		b.g("shirt-shadow", shadeHex(outfit, 30), path(fmt.Sprintf("M%g,300 L%g,566 L%g,566 L%g,300 Z", cx-3, cx-3, cx+3, cx+3)))
	case "mechanic", "farmer":
		// Overalls: bib and straps over the shirt; trousers share the colour.
		b.g("overall-base", outfit, path(fmt.Sprintf("M%g,400 L%g,400 L%g,566 L%g,566 Z", cx-44, cx+44, cx+wa+2, cx-wa-2)))
		b.raw(stroke(fmt.Sprintf("M%g,400 L%g,306 M%g,400 L%g,306", cx-38, cx-52, cx+38, cx+52), outfit, 10))
		b.g("button-base", "#d4af37", circ(cx-36, 404, 5), circ(cx+36, 404, 5))
		b.g("overall-shadow", shadeHex(outfit, -28), path(fmt.Sprintf("M%g,430 L%g,430 L%g,470 L%g,470 Z", cx-22, cx+22, cx+22, cx-22)))
	case "builder":
		// Hi-vis vest with reflective stripes.
		vest := func(s float64) string {
			return path(fmt.Sprintf("M%g,302 L%g,304 Q%g,306 %g,330 L%g,566 L%g,566 Z", cx+s*24, cx+s*(sh-30), cx+s*(sh-6), cx+s*sh, cx+s*wa, cx+s*16))
		}
		b.g("vest-base", outfit, vest(-1), vest(1))
		b.g("stripe-base", "#e0e0e0", path(fmt.Sprintf("M%g,440 L%g,440 L%g,456 L%g,456 Z M%g,440 L%g,440 L%g,456 L%g,456 Z", cx-wa-6, cx-16, cx-16, cx-wa-4, cx+16, cx+wa+6, cx+wa+4, cx+16)))
	case "artist":
		b.g("paint-base", "#e74c3c", circ(cx-30, 420, 6), circ(cx+24, 470, 5))
		b.g("paint-shadow", "#3498db", circ(cx+10, 390, 5), circ(cx-14, 500, 6))
		b.g("pocket-base", shadeHex(outfit, -24), path(fmt.Sprintf("M%g,480 h46 v34 h-46 Z", cx+6)))
	case "student":
		b.raw(stroke(fmt.Sprintf("M%g,306 L%g,520 M%g,306 L%g,520", cx-50, cx-46, cx+50, cx+46), "#2c3e50", 9))
	case "nurse", "casual", "customer":
		b.g("shirt-shadow", shadeHex(outfit, -28), path(fmt.Sprintf("M%g,300 Q200,330 %g,300 L%g,306 Q200,340 %g,306 Z", cx-28, cx+28, cx+30, cx-30)))
	}
	if spec.Role == "doctor" {
		b.raw(stroke(fmt.Sprintf("M%g,304 C%g,380 %g,400 %g,420 M%g,304 C%g,380 %g,400 %g,420 M%g,420 C%g,450 %g,450 %g,420", cx-30, cx-34, cx-24, cx-12, cx+30, cx+34, cx+24, cx+12, cx-12, cx-6, cx+6, cx+12), "#3c3c44", 4))
		b.g("steth-base", "#9aa0a8", circ(cx, 452, 9))
	}
	if spec.Role == "nurse" {
		b.g("badge-base", "#ffffff", path(fmt.Sprintf("M%g,370 h22 v14 h-22 Z", cx+30)))
	}

	// Arms: sleeve (long or short) and hand, hanging at the sides.
	sleeveID := "shirt-base"
	if coatRoles[spec.Role] {
		sleeveID = "coat-base"
	}
	arm := func(id string, s float64) {
		x := cx + s*sh
		end := 560.0
		if shortSleeve {
			end = 410
		}
		sl := path(fmt.Sprintf("M%g,326 Q%g,316 %g,330 L%g,%g L%g,%g Z", x-s*30, x-s*6, x+s*6, x+s*22, end, x-s*14, end))
		fmt.Fprintf(&b, "<g id=%q>", id)
		if shortSleeve {
			b.g("skin-base", skin, path(fmt.Sprintf("M%g,400 L%g,400 L%g,562 L%g,562 Z", x-s*12, x+s*20, x+s*20, x-s*6)))
		}
		b.g(sleeveID, sleeve, sl)
		if spec.Role == "firefighter" {
			b.g("stripe-base", "#f2d230", path(fmt.Sprintf("M%g,520 L%g,520 L%g,534 L%g,534 Z", x-s*13, x+s*21, x+s*21, x-s*13)))
		}
		b.g("skin-base", skin, ell(x+s*8, 582, 19, 22))
		// Props held in a hand.
		if s < 0 && spec.Role == "customer" {
			b.g("bag-base", "#e8c07a", path(fmt.Sprintf("M%g,590 L%g,590 L%g,680 L%g,680 Z", x-34, x+46, x+52, x-40)))
			b.raw(stroke(fmt.Sprintf("M%g,592 C%g,556 %g,556 %g,592", x-14, x-10, x+22, x+26), "#b8904a", 5))
		}
		if s > 0 && spec.Role == "singer" {
			b.g("mic-base", "#2b2b2e", path(fmt.Sprintf("M%g,560 L%g,560 L%g,610 L%g,610 Z", x+3, x+13, x+11, x+5)))
			b.g("mic-shadow", "#9aa0a8", circ(x+8, 552, 12))
		}
		if s > 0 && spec.Role == "mechanic" {
			b.raw(stroke(fmt.Sprintf("M%g,590 L%g,660", x+10, x+16), "#9aa0a8", 9))
			b.raw(stroke(fmt.Sprintf("M%g,664 a10,10 0 1,0 6,-2", x+8), "#9aa0a8", 6))
		}
		b.raw("</g>")
	}
	arm("arm-left", -1)
	arm("arm-right", 1)

	// Head.
	b.raw(`<g id="head-base">`)
	b.g("skin-base", skin, path("M182,250 L218,250 L220,304 L180,304 Z"), ell(130, 196, 14, 22), ell(270, 196, 14, 22))
	b.g("skin-shadow", skinShadow, path("M182,262 L218,262 L218,290 Q200,300 182,290 Z"))
	face := map[bool]string{false: "M130,180 C130,110 160,92 200,92 C240,92 270,110 270,180 C270,250 240,282 200,282 C160,282 130,250 130,180 Z",
		true: "M134,182 C134,112 162,96 200,96 C238,96 266,112 266,182 C266,248 238,276 200,276 C162,276 134,248 134,182 Z"}[woman]
	b.g("skin-base", skin, path(face))
	if spec.Beard && !woman {
		b.g("hair-base", hair, path("M132,200 C136,250 166,290 200,290 C234,290 264,250 268,200 C262,236 244,252 230,250 C220,240 180,240 170,250 C156,252 138,236 132,200 Z"))
	}
	if spec.Moustache && !woman || spec.Beard && !woman {
		b.g("hair-base", hair, path("M176,232 C186,222 198,226 200,228 C202,226 214,222 224,232 C214,238 206,236 200,234 C194,236 186,238 176,232 Z"))
	}
	switch spec.HairStyle {
	case "short":
		b.g("hair-base", hair, path("M128,176 C122,110 160,80 202,80 C246,80 280,108 272,176 C266,140 246,124 226,122 C200,132 160,128 142,132 C134,146 130,160 128,176 Z"))
	case "side":
		b.g("hair-base", hair, path("M128,180 C120,108 162,80 204,82 C250,84 282,112 272,180 C262,140 250,128 236,120 C220,140 170,132 150,124 C138,140 132,158 128,180 Z"))
	case "curly":
		var c []string
		for _, p := range [][3]float64{{140, 150, 22}, {152, 118, 24}, {182, 98, 25}, {218, 98, 25}, {248, 118, 24}, {260, 150, 22}, {128, 182, 16}, {272, 182, 16}, {200, 104, 22}} {
			c = append(c, circ(p[0], p[1], p[2]))
		}
		b.g("hair-base", hair, c...)
	case "long":
		b.g("hair-base", hair, path("M128,190 C120,108 162,82 200,82 C240,82 280,108 272,190 C262,146 240,120 214,118 C196,136 160,140 138,150 C132,164 130,176 128,190 Z"))
	case "bun":
		b.g("hair-base", hair, circ(200, 80, 30), path("M130,180 C124,112 162,90 200,90 C238,90 276,112 270,180 C262,142 238,126 200,126 C162,126 138,142 130,180 Z"))
	case "ponytail":
		b.g("hair-base", hair, path("M130,180 C124,112 162,88 200,88 C238,88 276,112 270,180 C262,142 238,124 200,124 C162,124 138,142 130,180 Z"))
	case "bald":
		b.g("hair-base", hair, path("M130,196 C128,170 132,156 138,150 L140,200 Z"), path("M270,196 C272,170 268,156 262,150 L260,200 Z"))
	}
	if spec.Role == "chef" {
		b.g("hat-base", "#ffffff", circ(172, 70, 26), circ(200, 58, 30), circ(228, 70, 26), path("M150,78 L250,78 L246,118 L154,118 Z"))
		b.g("hat-shadow", "#dcdcdc", path("M154,108 L246,108 L246,118 L154,118 Z"))
	}
	if spec.Role == "police" {
		b.g("cap-base", shadeHex(outfit, -10), path("M134,124 C140,84 260,84 266,124 Z"), path("M126,124 L274,124 L280,138 L120,138 Z"))
		b.g("badge-base", "#d4af37", circ(200, 108, 8))
	}
	switch spec.Role {
	case "firefighter":
		b.g("helmet-base", "#c0392b", path("M126,150 C126,70 274,70 274,150 Z"), path("M112,146 L288,146 L296,162 L104,162 Z"))
		b.g("badge-base", "#f2d230", path("M190,92 h20 v30 h-20 Z"))
	case "builder":
		b.g("helmet-base", "#f2c230", path("M130,140 C130,70 270,70 270,140 Z"), path("M118,136 L282,136 L286,150 L114,150 Z"))
		b.g("helmet-shadow", "#d9a51f", path("M194,74 h12 v62 h-12 Z"))
	case "pilot":
		b.g("cap-base", outfit, path("M134,124 C140,84 260,84 266,124 Z"), path("M126,124 L274,124 L280,138 L120,138 Z"))
		b.g("badge-base", "#d4af37", path("M186,100 h28 v10 h-28 Z"))
	case "farmer":
		b.g("hat-base", "#d9b25f", path("M150,120 C150,70 250,70 250,120 Z"), ell(200, 124, 96, 16))
		b.g("hat-shadow", "#b08a3e", path("M150,108 L250,108 L250,118 L150,118 Z"))
	case "artist":
		b.g("hat-base", "#c0392b", path("M128,128 C120,86 220,66 270,104 C276,118 260,128 240,124 C200,116 160,120 128,128 Z"), circ(236, 80, 6))
	case "scientist":
		b.g("goggle-base", "#6fb8d8", ell(172, 118, 18, 11), ell(228, 118, 18, 11))
		b.raw(stroke("M130,118 L154,118 M190,118 L210,118 M246,118 L270,118", "#2c2c30", 5))
	case "nurse":
		b.g("cap-base", outfit, path("M152,104 C160,84 240,84 248,104 L240,118 L160,118 Z"))
	}
	// Brows and nose.
	b.g("hair-shadow", shadeHex(hair, 10), path("M158,160 Q172,150 188,158 L188,164 Q172,158 158,166 Z"), path("M212,158 Q228,150 242,160 L242,166 Q228,158 212,164 Z"))
	b.raw(stroke("M200,186 Q196,208 194,214 Q200,220 208,214", skinShadow, 4))
	if spec.Glasses {
		b.raw(stroke("M152,186 a20,18 0 1,0 40,0 a20,18 0 1,0 -40,0 M208,186 a20,18 0 1,0 40,0 a20,18 0 1,0 -40,0 M192,186 L208,186", "#2c2c30", 4))
	}
	b.raw("</g>")

	// Eyes.
	b.raw(`<g id="eyes-open">`)
	b.g("whites-base", "#ffffff", ell(172, 188, 13, 9), ell(228, 188, 13, 9))
	b.g("iris-base", line, circ(173, 189, 6.5), circ(229, 189, 6.5))
	b.g("glint-base", "#ffffff", circ(175, 186, 2), circ(231, 186, 2))
	b.raw("</g>")
	b.raw(`<g id="eyes-closed" opacity="0">`)
	b.g("skin-base", skin, ell(172, 188, 16, 12), ell(228, 188, 16, 12))
	b.raw(stroke("M158,188 Q172,196 186,188 M214,188 Q228,196 242,188", line, 3.5))
	b.raw("</g>")

	// Mouth.
	lip := map[bool]string{false: "#8a4038", true: "#b8475a"}[woman]
	b.raw(`<g id="mouth-closed">`)
	b.raw(stroke("M184,244 Q200,256 216,244", lip, 4))
	b.raw("</g>")
	b.raw(`<g id="mouth-open" opacity="0">`)
	b.g("skin-base", skin, ell(200, 248, 20, 10))
	b.g("mouth-base", "#4a1d1d", path("M184,242 Q200,240 216,242 Q214,262 200,264 Q186,262 184,242 Z"))
	b.g("tongue-base", "#d0606a", path("M190,256 Q200,250 210,256 Q206,263 200,263 Q194,263 190,256 Z"))
	b.raw("</g>")
	if spec.Moustache && !woman || spec.Beard && !woman {
		// The moustache sits over both mouth frames.
		b.g("hair-base", hair, path("M176,232 C186,222 198,226 200,228 C202,226 214,222 224,232 C214,238 206,236 200,234 C194,236 186,238 176,232 Z"))
	}
	b.raw("</svg>")
	return []byte(b.String()), nil
}
