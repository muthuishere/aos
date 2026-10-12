package engine

// User defaults for `aos draw`: font and look, read from
// ~/.config/aos/draw.json (same path on macOS and Windows; $AOS_CONFIG
// overrides the file, $XDG_CONFIG_HOME the directory).
//
// Migration from windowctl: when $AOS_CONFIG is unset and draw.json does
// not exist, $WINDOWCTL_CONFIG and then ~/.config/windowctl/fontconfig.json
// are read instead, so an existing windowctl setup keeps working.
// Precedence: explicit DrawOptions / CLI flag > config file > built-in.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// DrawConfig is the on-disk shape of draw.json. Every field is
// optional; empty means "use the built-in default".
type DrawConfig struct {
	// Font is a font file path (.ttf/.otf/.ttc) or a font name looked up
	// in the system font directories, e.g. "Arial Rounded Bold",
	// "Segoe UI Black", "Avenir Next Heavy".
	Font string `json:"font,omitempty"`
	// FontScale multiplies the S/M/L font sizes (1 = bigarrow's sizes).
	FontScale float64 `json:"fontScale,omitempty"`

	Color       string `json:"color,omitempty"` // preset or #RRGGBB
	TextColor   string `json:"textColor,omitempty"`
	BorderColor string `json:"borderColor,omitempty"`
	EdgeColor   string `json:"edgeColor,omitempty"`

	Size    string `json:"size,omitempty"`    // S, M, L
	Style   string `json:"style,omitempty"`   // arrow, ring, box
	Shape   string `json:"shape,omitempty"`   // bend, straight, zigzag, spiral
	Border  string `json:"border,omitempty"`  // shadow, white-black, black
	Corners string `json:"corners,omitempty"` // round, sharp
	From    string `json:"from,omitempty"`    // auto, top-left, ...
	// Animate is "on" / "off" (or true / false): the entrance, pulse and
	// fade-out of a shown drawing.
	Animate AnimateSetting `json:"animate,omitempty"`

	// Text and Subtitle are the defaults of `draw --style text` and of
	// subtitles. The top-level look keys above apply to arrow / ring /
	// box only (and font / fontScale to all, unless set here).
	Text     *TextConfig `json:"text,omitempty"`
	Subtitle *TextConfig `json:"subtitle,omitempty"`
}

// TextConfig is the defaults block of a text drawing or subtitle.
type TextConfig struct {
	Color    string `json:"color,omitempty"`    // text colour: preset or #RRGGBB
	Bg       string `json:"bg,omitempty"`       // auto, none, or a colour
	Font     string `json:"font,omitempty"`     // file path or system font name
	Size     string `json:"size,omitempty"`     // S, M, L, XL
	Position string `json:"position,omitempty"` // bottom-center, top-center, center, top-left, ...
	Align    string `json:"align,omitempty"`    // left, center, right
	Weight   string `json:"weight,omitempty"`   // regular, bold
	Margin   string `json:"margin,omitempty"`   // px or N%
	MaxWidth string `json:"maxWidth,omitempty"` // px or N%
}

// AnimateSetting reads "on" / "off" or a JSON boolean.
type AnimateSetting string

func (a *AnimateSetting) UnmarshalJSON(b []byte) error {
	var v bool
	if json.Unmarshal(b, &v) == nil {
		*a = "off"
		if v {
			*a = "on"
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("animate must be \"on\", \"off\", true or false")
	}
	*a = AnimateSetting(s)
	return nil
}

// DefaultDrawConfig is what `windowctl draw config --init` writes.
func DefaultDrawConfig() DrawConfig {
	font := "Arial Rounded Bold"
	if runtime.GOOS == "windows" {
		font = "Segoe UI Black"
	}
	return DrawConfig{
		Font: font, FontScale: 1, Color: "red", Size: "M", Style: "arrow",
		Shape: "bend", Border: "shadow", Corners: "round", From: "auto", Animate: "on",
		Text:     &TextConfig{Color: "white", Bg: "auto", Size: "M", Position: "bottom-center"},
		Subtitle: &TextConfig{Color: "white", Bg: "auto", Position: "bottom-center"},
	}
}

// DrawConfigPath is where the config is read from: $AOS_CONFIG, else
// ~/.config/aos/draw.json; if that is absent, the legacy windowctl
// locations ($WINDOWCTL_CONFIG, ~/.config/windowctl/fontconfig.json) are
// used when they exist. With nothing on disk the aos path is returned.
func DrawConfigPath() string {
	if p := os.Getenv("AOS_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	primary := filepath.Join(dir, "aos", "draw.json")
	if fileExists(primary) {
		return primary
	}
	if p := os.Getenv("WINDOWCTL_CONFIG"); p != "" {
		return p
	}
	if legacy := filepath.Join(dir, "windowctl", "fontconfig.json"); fileExists(legacy) {
		return legacy
	}
	return primary
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

var (
	drawCfgOnce sync.Once
	drawCfg     DrawConfig
	drawCfgErr  error
)

// LoadDrawConfig reads the config file; a missing file is not an error.
func LoadDrawConfig() (DrawConfig, error) {
	drawCfgOnce.Do(func() {
		b, err := os.ReadFile(DrawConfigPath())
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			drawCfgErr = err
			return
		}
		if err := json.Unmarshal(b, &drawCfg); err != nil {
			drawCfgErr = fmt.Errorf("draw: %s is not valid JSON: %w", DrawConfigPath(), err)
		}
	})
	return drawCfg, drawCfgErr
}

// InitDrawConfig writes DefaultDrawConfig to DrawConfigPath unless a
// file is already there (force overwrites). Returns the path.
func InitDrawConfig(force bool) (string, error) {
	p := DrawConfigPath()
	if _, err := os.Stat(p); err == nil && !force {
		return p, fmt.Errorf("draw: %s already exists (use --force to overwrite)", p)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return p, err
	}
	b, _ := json.MarshalIndent(DefaultDrawConfig(), "", "  ")
	return p, os.WriteFile(p, append(b, '\n'), 0o644)
}

// applyConfig fills every unset option from the config file.
func (o *DrawOptions) applyConfig(c DrawConfig) error {
	fill := func(dst *string, v string) {
		if *dst == "" {
			*dst = v
		}
	}
	if o.Style == DrawText {
		t, name := c.Text, "text"
		if o.Subtitle {
			t, name = c.Subtitle, "subtitle"
		}
		if t != nil {
			fill((*string)(&o.Size), t.Size)
			fill((*string)(&o.Position), t.Position)
			fill(&o.Background, t.Bg)
			fill(&o.Align, t.Align)
			fill(&o.Weight, t.Weight)
			fill(&o.Margin, t.Margin)
			fill(&o.MaxWidth, t.MaxWidth)
			fill(&o.Font, t.Font)
			if o.Color == nil && t.Color != "" {
				col, err := ParseColor(t.Color)
				if err != nil {
					return fmt.Errorf("draw: %s.color in %s: %w", name, DrawConfigPath(), err)
				}
				o.Color = &col
			}
		}
		fill((*string)(&o.Corners), c.Corners)
		fill((*string)(&o.Animate), string(c.Animate))
		fill(&o.Font, c.Font)
		if o.FontScale == 0 {
			o.FontScale = c.FontScale
		}
		return nil
	}
	fill((*string)(&o.Style), c.Style)
	fill((*string)(&o.Shape), c.Shape)
	fill((*string)(&o.Size), c.Size)
	fill((*string)(&o.Border), c.Border)
	fill((*string)(&o.Corners), c.Corners)
	fill((*string)(&o.From), c.From)
	fill((*string)(&o.Animate), string(c.Animate))
	for _, f := range []struct {
		dst  **Color
		v    string
		name string
	}{{&o.Color, c.Color, "color"}, {&o.TextColor, c.TextColor, "textColor"},
		{&o.BorderColor, c.BorderColor, "borderColor"}, {&o.EdgeColor, c.EdgeColor, "edgeColor"}} {
		if *f.dst != nil || f.v == "" {
			continue
		}
		col, err := ParseColor(f.v)
		if err != nil {
			return fmt.Errorf("draw: %s in %s: %w", f.name, DrawConfigPath(), err)
		}
		*f.dst = &col
	}
	if o.Font == "" {
		o.Font = c.Font
	}
	if o.FontScale == 0 {
		o.FontScale = c.FontScale
	}
	return nil
}

// fontDirs are the system font directories searched for a font name.
func fontDirs() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"/System/Library/Fonts", "/System/Library/Fonts/Supplemental", "/Library/Fonts", filepath.Join(home, "Library/Fonts")}
	case "windows":
		win := os.Getenv("WINDIR")
		if win == "" {
			win = `C:\Windows`
		}
		return []string{filepath.Join(win, "Fonts"), filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\Windows\Fonts`)}
	}
	return []string{"/usr/share/fonts", "/usr/local/share/fonts", filepath.Join(home, ".local/share/fonts")}
}

func normFontName(s string) string {
	s = strings.ToLower(strings.TrimSuffix(s, filepath.Ext(s)))
	return strings.NewReplacer(" ", "", "-", "", "_", "").Replace(s)
}

// resolveFontPath maps a config/flag font value to a file: a path is
// used as-is; a name is matched against font file names (exact, then
// prefix) in the system font directories.
func resolveFontPath(name string) (string, error) {
	if name == "" {
		return "", nil
	}
	if strings.ContainsAny(name, `/\`) || filepath.Ext(name) != "" {
		if _, err := os.Stat(name); err != nil {
			return "", fmt.Errorf("draw: font file %s: %w", name, err)
		}
		return name, nil
	}
	want := normFontName(name)
	var prefix string
	for _, dir := range fontDirs() {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".ttf", ".otf", ".ttc":
			default:
				return nil
			}
			got := normFontName(d.Name())
			if got == want {
				prefix = p
				return filepath.SkipAll
			}
			if prefix == "" && strings.HasPrefix(got, want) {
				prefix = p
			}
			return nil
		})
		if prefix != "" && normFontName(filepath.Base(prefix)) == want {
			break
		}
	}
	if prefix == "" {
		return "", fmt.Errorf("draw: font %q not found in %s — give a file path, or run `aos draw fonts` to list names", name, strings.Join(fontDirs(), ", "))
	}
	return prefix, nil
}

// ListFonts returns the font names (file names without extension)
// `--font` / the config's "font" can use.
func ListFonts() []string {
	seen := map[string]bool{}
	var out []string
	for _, dir := range fontDirs() {
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			switch strings.ToLower(filepath.Ext(p)) {
			case ".ttf", ".otf", ".ttc":
				n := strings.TrimSuffix(d.Name(), filepath.Ext(d.Name()))
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
			return nil
		})
	}
	return out
}
