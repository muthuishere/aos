package engine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Character SVGs (assets/*.svg) are rigged with named groups: frames are
// groups drawn with opacity="0" until shown (eyes-closed, mouth-open), and
// colours are groups whose id is "<part>-<tone>" (shirt-base, skin-shadow).
// The rasterizer ignores group opacity, so a hidden group is cut out of the
// document before drawing, never just left transparent.

// SVGParts picks what a rigged SVG draws: Show turns on opacity-0 groups by
// id, Hide removes groups by id, Recolor sets a part's base colour (the
// shadow, deep and highlight tones are derived from it).
type SVGParts struct {
	Show, Hide []string
	Recolor    map[string]string
}

func (p SVGParts) empty() bool { return len(p.Show) == 0 && len(p.Hide) == 0 && len(p.Recolor) == 0 }

var (
	svgTagRe   = regexp.MustCompile(`<(/?)([A-Za-z][\w:-]*)((?:[^>"']|"[^"]*"|'[^']*')*?)(/?)>`)
	svgIDRe    = regexp.MustCompile(`\sid="([^"]*)"`)
	svgOpac0Re = regexp.MustCompile(`\sopacity="0(?:\.0*)?"`)
	svgFillRe  = regexp.MustCompile(`(\sfill=")(#[0-9A-Fa-f]{6}|#[0-9A-Fa-f]{3})(")`)
	hexRe      = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)
)

// toneShift is how far each tone sits from the base colour, per channel.
var toneShift = map[string]int{"base": 0, "shadow": -28, "deep": -48, "highlight": 36}

// ApplySVGParts returns data with p applied. Groups with opacity="0" that
// are not in Show are removed, so they never draw. An id in Show or Hide
// that the file lacks is an error naming the ids it has.
func ApplySVGParts(data []byte, p SVGParts) ([]byte, error) {
	for part, c := range p.Recolor {
		if !hexRe.MatchString(c) {
			return nil, fmt.Errorf("draw: --recolor %s=%q: want a #rrggbb colour", part, c)
		}
	}
	show, hide := set(p.Show), set(p.Hide)
	seen := map[string]bool{}
	src := string(data)
	var out strings.Builder
	last, skipDepth, depth := 0, -1, 0
	for _, m := range svgTagRe.FindAllStringSubmatchIndex(src, -1) {
		closing := m[3] > m[2]
		name := src[m[4]:m[5]]
		attrs := src[m[6]:m[7]]
		selfClose := m[9] > m[8]
		if skipDepth >= 0 {
			if !closing && !selfClose {
				depth++
			} else if closing {
				depth--
				if depth == skipDepth {
					skipDepth = -1
					last = m[1]
				}
			}
			continue
		}
		if closing {
			depth--
			continue
		}
		id := ""
		if im := svgIDRe.FindStringSubmatch(attrs); im != nil {
			id = im[1]
			seen[id] = true
		}
		if name == "g" && id != "" {
			hidden := svgOpac0Re.MatchString(attrs)
			if hide[id] || (hidden && !show[id]) {
				out.WriteString(src[last:m[0]])
				if selfClose {
					last = m[1]
				} else {
					skipDepth = depth
					depth++
				}
				continue
			}
			newAttrs := attrs
			if show[id] {
				newAttrs = svgOpac0Re.ReplaceAllString(newAttrs, "")
			}
			if part, tone, ok := strings.Cut(id, "-"); ok {
				if base, ok := p.Recolor[part]; ok {
					if shift, ok := toneShift[tone]; ok {
						newAttrs = svgFillRe.ReplaceAllString(newAttrs, "${1}"+shadeHex(base, shift)+"${3}")
					}
				}
			}
			if newAttrs != attrs {
				out.WriteString(src[last:m[6]])
				out.WriteString(newAttrs)
				last = m[7]
			}
		}
		if !selfClose {
			depth++
		}
	}
	out.WriteString(src[last:])
	var missing []string
	for _, id := range append(append([]string{}, p.Show...), p.Hide...) {
		if !seen[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("draw: this SVG has no group %s; it has: %s", strings.Join(missing, ", "), strings.Join(rigGroups(seen), ", "))
	}
	parts := map[string]bool{}
	for id := range seen {
		if part, tone, ok := strings.Cut(id, "-"); ok {
			if _, ok := toneShift[tone]; ok {
				parts[part] = true
			}
		}
	}
	for part := range p.Recolor {
		if !parts[part] {
			return nil, fmt.Errorf("draw: --recolor %s: this SVG has no %s-base group", part, part)
		}
	}
	return []byte(out.String()), nil
}

func set(xs []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func rigGroups(seen map[string]bool) []string {
	var ids []string
	for id := range seen {
		if strings.Contains(id, "-") {
			ids = append(ids, id)
		}
	}
	sortStrings(ids)
	return ids
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func shadeHex(hex string, amount int) string {
	hex = strings.TrimPrefix(hex, "#")
	out := "#"
	for i := 0; i < 6; i += 2 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		c := min(255, max(0, int(v)+amount))
		out += fmt.Sprintf("%02x", c)
	}
	return out
}
