package groups

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

// displayMode is one resolution a monitor supports, in `display modes --json` shape.
type displayMode struct {
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	Refresh float64 `json:"refresh_hz"`
	Current bool    `json:"current"`
}

func (m displayMode) String() string {
	s := fmt.Sprintf("%dx%d@%g", m.Width, m.Height, m.Refresh)
	if m.Current {
		s += "  (current)"
	}
	return s
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "display", Name: "modes",
				Summary: "List the resolutions and refresh rates a monitor supports",
				Args:    "<monitor id> [--json]",
				Examples: []string{
					"aos display modes 1",
					"aos display modes 2 --json",
				},
				Run: runDisplayModes,
			},
			&cli.Command{
				Group: "display", Name: "mode",
				Summary: "Switch a monitor to a resolution (highest refresh unless @Hz is given)",
				Args:    "<monitor id> <WxH[@Hz]>",
				Examples: []string{
					"aos display mode 2 2560x1440",
					"aos display mode 1 1920x1080@60",
				},
				Run: runDisplayMode,
			},
			&cli.Command{
				Group: "display", Name: "mirror",
				Summary: "Mirror every monitor to the main one (on) or extend the desktop (off)",
				Args:    "on|off",
				Examples: []string{
					"aos display mirror on",
					"aos display mirror off",
				},
				Run: runDisplayMirror,
			},
		)
	})
}

// monitorByID finds an aos monitor (1-indexed, as `display list` shows).
func monitorByID(arg string) (engine.Monitor, error) {
	id, err := strconv.Atoi(arg)
	if err != nil {
		return engine.Monitor{}, fmt.Errorf("monitor id must be a number from `aos display list`, got %q", arg)
	}
	ms, err := engine.ListMonitors()
	if err != nil {
		return engine.Monitor{}, err
	}
	for _, m := range ms {
		if m.ID == id {
			return m, nil
		}
	}
	return engine.Monitor{}, &cli.ExitError{Code: 3, Message: fmt.Sprintf("no monitor %d; see `aos display list`", id)}
}

func runDisplayModes(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`display modes` needs a monitor id, e.g. aos display modes 1")
	}
	mon, err := monitorByID(set.Rest[0])
	if err != nil {
		return err
	}
	modes, err := listDisplayModes(mon)
	if err != nil {
		return err
	}
	modes = sortModes(modes)
	if set.Has("json") {
		if modes == nil {
			modes = []displayMode{}
		}
		return writeJSON(c, modes)
	}
	for _, m := range modes {
		c.Println(m.String())
	}
	return nil
}

func runDisplayMode(c *cli.Ctx, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("`display mode` needs a monitor id and a mode, e.g. aos display mode 2 2560x1440@60")
	}
	w, h, hz, err := parseModeSpec(args[1])
	if err != nil {
		return err
	}
	mon, err := monitorByID(args[0])
	if err != nil {
		return err
	}
	modes, err := listDisplayModes(mon)
	if err != nil {
		return err
	}
	m, err := pickMode(modes, w, h, hz)
	if err != nil {
		return err
	}
	if err := setDisplayMode(mon, m); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	c.Printf("monitor %d now %dx%d@%g\n", mon.ID, m.Width, m.Height, m.Refresh)
	return nil
}

func runDisplayMirror(c *cli.Ctx, args []string) error {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		return fmt.Errorf("`display mirror` takes on or off")
	}
	if err := setMirror(args[0] == "on"); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	c.Printf("mirroring %s\n", args[0])
	return nil
}

// parseModeSpec reads "2560x1440" or "1920x1080@60" (hz 0 means any).
func parseModeSpec(s string) (int, int, float64, error) {
	bad := fmt.Errorf("mode must look like 2560x1440 or 1920x1080@60, got %q", s)
	res, rate, hasRate := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "@")
	ws, hs, ok := strings.Cut(res, "x")
	if !ok {
		return 0, 0, 0, bad
	}
	w, err1 := strconv.Atoi(ws)
	h, err2 := strconv.Atoi(hs)
	if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
		return 0, 0, 0, bad
	}
	hz := 0.0
	if hasRate {
		v, err := strconv.ParseFloat(strings.TrimSuffix(rate, "hz"), 64)
		if err != nil || v <= 0 {
			return 0, 0, 0, bad
		}
		hz = v
	}
	return w, h, hz, nil
}

// pickMode chooses the exact WxH; with hz, the mode within 0.5 Hz of it,
// otherwise the highest refresh. Anything else is refused with the choices.
func pickMode(modes []displayMode, w, h int, hz float64) (displayMode, error) {
	var best *displayMode
	for i := range modes {
		m := modes[i]
		if m.Width != w || m.Height != h {
			continue
		}
		if hz > 0 && math.Abs(m.Refresh-hz) > 0.5 {
			continue
		}
		if best == nil || m.Refresh > best.Refresh {
			best = &modes[i]
		}
	}
	if best != nil {
		return *best, nil
	}
	var have []string
	for _, m := range sortModes(append([]displayMode(nil), modes...)) {
		m.Current = false
		have = append(have, m.String())
	}
	want := fmt.Sprintf("%dx%d", w, h)
	if hz > 0 {
		want += fmt.Sprintf("@%g", hz)
	}
	return displayMode{}, fmt.Errorf("monitor does not support %s; it supports: %s", want, strings.Join(have, ", "))
}

// sortModes orders largest first, then fastest, and drops duplicates.
func sortModes(modes []displayMode) []displayMode {
	sort.SliceStable(modes, func(i, j int) bool {
		a, b := modes[i], modes[j]
		if a.Width*a.Height != b.Width*b.Height {
			return a.Width*a.Height > b.Width*b.Height
		}
		return a.Refresh > b.Refresh
	})
	var out []displayMode
	for _, m := range modes {
		if n := len(out); n > 0 && out[n-1].Width == m.Width && out[n-1].Height == m.Height && out[n-1].Refresh == m.Refresh {
			out[n-1].Current = out[n-1].Current || m.Current
			continue
		}
		out = append(out, m)
	}
	return out
}

// xrandrOutput is one connected output from `xrandr --query`.
type xrandrOutput struct {
	Name  string
	X, Y  int
	Modes []displayMode
}

// parseXrandr reads `xrandr --query`: "HDMI-1 connected primary 2560x1440+0+0 ..."
// followed by indented "   2560x1440     59.95*+  30.00" mode lines.
func parseXrandr(out string) []xrandrOutput {
	var outs []xrandrOutput
	var cur *xrandrOutput
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if line[0] != ' ' {
			cur = nil
			f := strings.Fields(line)
			if len(f) >= 2 && f[1] == "connected" {
				o := xrandrOutput{Name: f[0]}
				for _, tok := range f[2:] {
					var w, h, x, y int
					if n, _ := fmt.Sscanf(tok, "%dx%d+%d+%d", &w, &h, &x, &y); n == 4 {
						o.X, o.Y = x, y
						break
					}
				}
				outs = append(outs, o)
				cur = &outs[len(outs)-1]
			}
			continue
		}
		if cur == nil {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		ws, hs, ok := strings.Cut(strings.TrimRight(f[0], "i"), "x")
		w, err1 := strconv.Atoi(ws)
		h, err2 := strconv.Atoi(hs)
		if !ok || err1 != nil || err2 != nil {
			continue
		}
		for _, r := range f[1:] {
			hz, err := strconv.ParseFloat(strings.Trim(r, "*+"), 64)
			if err != nil {
				continue
			}
			cur.Modes = append(cur.Modes, displayMode{Width: w, Height: h, Refresh: hz, Current: strings.Contains(r, "*")})
		}
	}
	return outs
}
