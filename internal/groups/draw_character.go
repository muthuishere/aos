package groups

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func runDrawCharacter(c *cli.Ctx, args []string) error {
	opts, set, err := drawOptionsFrom(args)
	if err != nil {
		return err
	}
	if set.Has("list") {
		return listCharacters(c, set.Has("json"))
	}
	spec := engine.CharacterSpec{}
	switch len(set.Rest) {
	case 0:
	case 1:
		preset, ok := engine.CharacterPresets[strings.ToLower(set.Rest[0])]
		if !ok {
			return fmt.Errorf("draw character: no preset %q; presets: %s (or use --role)", set.Rest[0], strings.Join(engine.CharacterPresetNames(), ", "))
		}
		spec = preset
	default:
		return fmt.Errorf("draw character: give at most one preset name")
	}
	if opts.Match != nil && set.String("app", "") == "" && set.String("title", "") == "" {
		opts.Match = nil // the word was the preset, not an app
	}
	for flag, field := range map[string]*string{"role": &spec.Role, "gender": &spec.Gender, "skin": &spec.Skin, "hair": &spec.Hair, "hair-style": &spec.HairStyle, "outfit": &spec.Outfit} {
		if v := set.String(flag, ""); v != "" {
			*field = v
		}
	}
	spec.Beard = spec.Beard || set.Has("beard")
	spec.Moustache = spec.Moustache || set.Has("moustache")
	spec.Glasses = spec.Glasses || set.Has("glasses")
	svg, err := engine.BuildCharacter(spec)
	if err != nil {
		return err
	}
	switch frame := set.String("frame", "rest"); frame {
	case "rest":
	case "blink":
		svg, err = engine.ApplySVGParts(svg, engine.SVGParts{Show: []string{"eyes-closed"}, Hide: []string{"eyes-open"}})
	case "talk":
		svg, err = engine.ApplySVGParts(svg, engine.SVGParts{Show: []string{"mouth-open"}, Hide: []string{"mouth-closed"}})
	default:
		return fmt.Errorf("draw character: --frame must be rest, blink or talk, got %q", frame)
	}
	if err != nil {
		return err
	}
	if out := set.String("save", ""); out != "" {
		if err := os.WriteFile(expandHome(out), svg, 0o644); err != nil {
			return err
		}
		c.Println(out)
		return nil
	}
	if set.String("height", "") == "" && set.String("width", "") == "" {
		args = append(args, "--height=45%")
		set.flags["height"] = "45%"
	}
	if opts.Position == "" {
		opts.Position = "bottom-left"
	}
	return finishDrawImage(c, []string{"draw", "character"}, args, opts, set, svg)
}

func listCharacters(c *cli.Ctx, asJSON bool) error {
	type preset struct {
		Name string `json:"name"`
		engine.CharacterSpec
	}
	var presets []preset
	for _, n := range engine.CharacterPresetNames() {
		presets = append(presets, preset{n, engine.CharacterPresets[n]})
	}
	if asJSON {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"presets": presets, "roles": engine.CharacterRoles, "hairStyles": engine.CharacterHairStyles})
	}
	for _, p := range presets {
		extra := ""
		if p.Glasses {
			extra += " glasses"
		}
		if p.Beard {
			extra += " beard"
		}
		if p.Moustache {
			extra += " moustache"
		}
		c.Printf("%-8s %-11s %-6s skin=%-6s hair=%s %s%s\n", p.Name, p.Role, p.Gender, p.Skin, p.Hair, p.HairStyle, extra)
	}
	c.Printf("\nroles: %s\nhair styles: %s\n", strings.Join(engine.CharacterRoles, " "), strings.Join(engine.CharacterHairStyles, " "))
	return nil
}
