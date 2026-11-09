package groups

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/engine"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "display", Name: "brightness",
				Summary: "Print or set a monitor's brightness 0-100: built-in panel or external over DDC/CI (default: the focused window's monitor)",
				Args:    "[<monitor id>] [<0-100>|+<n>|-<n>] [--monitor=<id>] [--all] [--list] [--json]",
				Examples: []string{
					"aos display brightness            # BenQ GW2785TC (2): 64",
					"aos display brightness +20        # brighter on the focused window's monitor",
					"aos display brightness 2 50       # monitor 2 to 50",
					"aos display brightness --monitor=3",
					"aos display brightness --all 40",
					"aos display brightness --list",
					"aos display brightness -10 --json",
				},
				Run: runDisplayBrightness,
			},
			&cli.Command{
				Group: "display", Name: "of",
				Summary: "Print the monitor an app's or a window's window is on: id, name and brightness",
				Args:    "--app=<name> | --window=<id> [--json]",
				Examples: []string{
					"aos display of --app Safari          # 2  BenQ GW2785TC  brightness 64",
					"aos display of --window 12345 --json",
				},
				Run: runDisplayOf,
			},
		)
	})
}

// screen is one monitor as brightness sees it. Method is "builtin" or "ddc"
// when software can drive it; otherwise get is nil and Reason says why not.
type screen struct {
	ID     int
	Name   string
	Method string
	Reason string
	get    func() (int, error)
	set    func(int) error
}

// brightnessJSON is the `--json` shape for one monitor.
type brightnessJSON struct {
	Monitor      int    `json:"monitor"`
	Name         string `json:"name"`
	Value        *int   `json:"value"`
	Method       string `json:"method,omitempty"`
	Controllable bool   `json:"controllable"`
	Reason       string `json:"reason,omitempty"`
}

func (s screen) label() string { return fmt.Sprintf("%s (%d)", s.Name, s.ID) }

func (s screen) uncontrollable() error {
	reason := s.Reason
	if reason == "" {
		reason = "no software brightness control"
	}
	return &cli.ExitError{Code: 2, Message: fmt.Sprintf("%s: %s", s.label(), reason)}
}

func (s screen) read() (int, error) {
	if s.get == nil {
		return 0, s.uncontrollable()
	}
	v, err := s.get()
	if err != nil {
		return 0, &cli.ExitError{Code: 2, Message: fmt.Sprintf("%s: %v", s.label(), err)}
	}
	return v, nil
}

// write sets the level and reads it back, so a monitor that ignores the
// command is reported instead of claimed.
func (s screen) write(pct int) (int, error) {
	if s.set == nil {
		return 0, s.uncontrollable()
	}
	if err := s.set(pct); err != nil {
		return 0, &cli.ExitError{Code: 2, Message: fmt.Sprintf("%s: %v", s.label(), err)}
	}
	got, err := s.read()
	if err != nil {
		return 0, err
	}
	if d := got - pct; d > 2 || d < -2 {
		return got, &cli.ExitError{Code: 2, Message: fmt.Sprintf("%s: asked for %d but the monitor reports %d", s.label(), pct, got)}
	}
	return got, nil
}

// status reads a screen for --list / `display of`: a failed read turns into
// "not controllable" with the reason, never a made-up value.
func (s screen) status() brightnessJSON {
	j := brightnessJSON{Monitor: s.ID, Name: s.Name, Method: s.Method, Controllable: s.get != nil, Reason: s.Reason}
	if s.get == nil {
		if j.Reason == "" {
			j.Reason = "no software brightness control"
		}
		return j
	}
	v, err := s.read()
	if err != nil {
		j.Controllable = false
		j.Reason = strings.TrimPrefix(errMessage(err), s.label()+": ")
		return j
	}
	j.Value = &v
	return j
}

func errMessage(err error) string {
	var ee *cli.ExitError
	if errors.As(err, &ee) {
		return ee.Message
	}
	return err.Error()
}

// pickScreen resolves an explicit id, else the monitor holding the focused
// window, else the one under the pointer, else the primary.
func pickScreen(all []screen, ms []engine.Monitor, id int) (screen, error) {
	if id == 0 {
		id = defaultMonitorID(ms)
	}
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
	}
	return screen{}, &cli.ExitError{Code: 3, Message: fmt.Sprintf("no monitor %d; see `aos display brightness --list`", id)}
}

func defaultMonitorID(ms []engine.Monitor) int {
	for _, want := range []func(engine.Monitor) bool{
		func(m engine.Monitor) bool { return m.Focused },
		func(m engine.Monitor) bool { return m.Active },
		func(m engine.Monitor) bool { return m.Primary },
	} {
		for _, m := range ms {
			if want(m) {
				return m.ID
			}
		}
	}
	if len(ms) > 0 {
		return ms[0].ID
	}
	return 0
}

func monitorsAndScreens() ([]engine.Monitor, []screen, error) {
	ms, err := engine.ListMonitors()
	if err != nil {
		return nil, nil, err
	}
	if len(ms) == 0 {
		return nil, nil, &cli.ExitError{Code: 1, Message: "no monitors detected"}
	}
	all, err := brightnessScreens(ms)
	return ms, all, err
}

func runDisplayBrightness(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "monitor")
	if err != nil {
		return err
	}
	if err := set.Reject("monitor", "all", "list", "json"); err != nil {
		return err
	}
	asJSON := set.Has("json")
	id, level, err := brightnessArgs(set)
	if err != nil {
		return err
	}
	if set.Has("list") && (level != "" || set.Has("all") || id != 0) {
		return fmt.Errorf("--list only reads every monitor; drop the id, level and --all")
	}
	if set.Has("all") && id != 0 {
		return fmt.Errorf("--all and a monitor id do not mix")
	}
	ms, all, err := monitorsAndScreens()
	if err != nil {
		return err
	}
	if set.Has("list") || (set.Has("all") && level == "") {
		return listBrightness(c, all, asJSON)
	}

	targets := all
	if !set.Has("all") {
		s, err := pickScreen(all, ms, id)
		if err != nil {
			return err
		}
		targets = []screen{s}
	}

	var out []brightnessJSON
	var failed error
	for _, s := range targets {
		v, err := s.read()
		if err == nil && level != "" {
			target, _ := brightnessTarget(level, v)
			v, err = s.write(target)
		}
		if err != nil {
			if len(targets) == 1 {
				return err
			}
			c.Warnf("%s\n", errMessage(err))
			failed = &cli.ExitError{Code: 2, Message: "some monitors could not be set"}
			j := s.status()
			out = append(out, j)
			continue
		}
		j := brightnessJSON{Monitor: s.ID, Name: s.Name, Value: &v, Method: s.Method, Controllable: true}
		out = append(out, j)
		if !asJSON {
			c.Printf("%s: %d\n", s.label(), v)
		}
	}
	if asJSON {
		var werr error
		if set.Has("all") {
			werr = writeJSON(c, out)
		} else {
			werr = writeJSON(c, out[0])
		}
		if werr != nil {
			return werr
		}
	}
	return failed
}

// brightnessArgs reads `[<id>] [<level>]` plus --monitor: one positional is a
// level, two are an id then a level.
func brightnessArgs(set *argSet) (int, string, error) {
	id, err := set.Int("monitor", 0)
	if err != nil {
		return 0, "", err
	}
	var level string
	switch len(set.Rest) {
	case 0:
	case 1:
		level = set.Rest[0]
	case 2:
		if set.Has("monitor") {
			return 0, "", fmt.Errorf("give the monitor either as --monitor or as the first argument, not both")
		}
		if id, err = strconv.Atoi(set.Rest[0]); err != nil || id < 1 {
			return 0, "", fmt.Errorf("monitor id must be a number from `aos display list`, got %q", set.Rest[0])
		}
		level = set.Rest[1]
	default:
		return 0, "", fmt.Errorf("`display brightness` takes [<monitor id>] [<level>]")
	}
	if level != "" {
		if _, err := brightnessTarget(level, 0); err != nil {
			return 0, "", err
		}
	}
	return id, level, nil
}

func listBrightness(c *cli.Ctx, all []screen, asJSON bool) error {
	out := make([]brightnessJSON, 0, len(all))
	for _, s := range all {
		out = append(out, s.status())
	}
	if asJSON {
		return writeJSON(c, out)
	}
	for _, j := range out {
		value, how := "-", j.Method
		if j.Value != nil {
			value = strconv.Itoa(*j.Value)
		}
		if !j.Controllable {
			how = "not controllable: " + j.Reason
		}
		c.Printf("%d  %-24s %4s  %s\n", j.Monitor, j.Name, value, how)
	}
	return nil
}

func runDisplayOf(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "app", "window")
	if err != nil {
		return err
	}
	if err := set.Reject("app", "window", "json"); err != nil {
		return err
	}
	app, win := set.String("app", ""), set.String("window", "")
	if (app == "") == (win == "") || len(set.Rest) > 0 {
		return fmt.Errorf("`display of` needs exactly one of --app <name> or --window <id>")
	}
	ws, err := engine.ListWindows(engine.Filter{App: app})
	if err != nil {
		return err
	}
	var hit *engine.Window
	for i := range ws {
		w := &ws[i]
		if (win != "" && w.ID != win) || w.Monitor == 0 {
			continue
		}
		if hit == nil || (w.Focused && !hit.Focused) {
			hit = w
		}
	}
	if hit == nil {
		what := "app " + strconv.Quote(app)
		if win != "" {
			what = "window " + win
		}
		return &cli.ExitError{Code: 3, Message: fmt.Sprintf("no on-screen window for %s; see `aos window list`", what)}
	}
	ms, all, err := monitorsAndScreens()
	if err != nil {
		return err
	}
	s, err := pickScreen(all, ms, hit.Monitor)
	if err != nil {
		return err
	}
	j := s.status()
	if set.Has("json") {
		return writeJSON(c, j)
	}
	b := "brightness -"
	if j.Value != nil {
		b = fmt.Sprintf("brightness %d", *j.Value)
	} else if j.Reason != "" {
		b += " (" + j.Reason + ")"
	}
	c.Printf("%d  %s  %s\n", s.ID, s.Name, b)
	return nil
}

// brightnessTarget resolves "50", "+10" or "-10" against the current level,
// clamped to 0-100: the same grammar as `audio volume`.
func brightnessTarget(arg string, current int) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(arg, "+"))
	if err != nil {
		return 0, fmt.Errorf("brightness must be a number, got %q", arg)
	}
	if strings.HasPrefix(arg, "+") || strings.HasPrefix(arg, "-") {
		n += current
	}
	return clamp(n, 0, 100), nil
}

// toPercent and fromPercent convert between a device's raw scale and 0-100.
func toPercent(raw, max int) int {
	if max <= 0 {
		return 0
	}
	return int(math.Round(float64(raw) * 100 / float64(max)))
}

func fromPercent(pct, max int) int {
	return int(math.Round(float64(pct) * float64(max) / 100))
}

// parseBrightnessctl reads `brightnessctl -m info`:
// "intel_backlight,backlight,400,42%,960".
func parseBrightnessctl(out string) (int, error) {
	f := strings.Split(strings.TrimSpace(strings.Split(out, "\n")[0]), ",")
	if len(f) < 5 {
		return 0, fmt.Errorf("unexpected brightnessctl output %q", out)
	}
	cur, err1 := strconv.Atoi(f[2])
	max, err2 := strconv.Atoi(f[4])
	if err1 != nil || err2 != nil {
		return 0, fmt.Errorf("unexpected brightnessctl output %q", out)
	}
	return toPercent(cur, max), nil
}

// ddcReply parses a DDC/CI "VCP feature reply" for feature vcp:
// [0x6E] 0x88 0x02 result vcp type maxH maxL curH curL chk. It scans for the
// 0x02 opcode so a transport that strips the leading address still parses.
func ddcReply(b []byte, vcp byte) (cur, max int, err error) {
	for i := 0; i+8 <= len(b); i++ {
		if b[i] != 0x02 || b[i+2] != vcp {
			continue
		}
		if b[i+1] != 0 {
			return 0, 0, fmt.Errorf("the monitor does not support DDC feature 0x%02X", vcp)
		}
		max = int(b[i+4])<<8 | int(b[i+5])
		cur = int(b[i+6])<<8 | int(b[i+7])
		if max == 0 {
			return 0, 0, fmt.Errorf("the monitor reported an empty brightness range")
		}
		return cur, max, nil
	}
	return 0, 0, fmt.Errorf("no valid DDC/CI reply (DDC/CI may be off in the monitor's menu)")
}

// ddcutilDisplay is one entry of `ddcutil detect --terse`.
type ddcutilDisplay struct {
	Number    int
	Connector string // DRM connector, e.g. card0-HDMI-A-1
	Model     string
}

// parseDdcutilDetect reads `ddcutil detect --terse`:
//
//	Display 1
//	   I2C bus:          /dev/i2c-4
//	   DRM connector:    card0-DP-1
//	   Monitor:          DEL:DELL U2415:CFV9N
func parseDdcutilDetect(out string) []ddcutilDisplay {
	var res []ddcutilDisplay
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if n, ok := strings.CutPrefix(t, "Display "); ok {
			if num, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				res = append(res, ddcutilDisplay{Number: num})
			}
			continue
		}
		if len(res) == 0 {
			continue
		}
		key, val, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "DRM connector":
			res[len(res)-1].Connector = val
		case "Monitor":
			if f := strings.Split(val, ":"); len(f) >= 2 && f[1] != "" {
				res[len(res)-1].Model = f[1]
			}
		}
	}
	return res
}

// matchDdcutil pairs an xrandr output (HDMI-1, DP-2) with a DRM connector
// (card0-HDMI-A-1, card1-DP-2).
func matchDdcutil(ds []ddcutilDisplay, output string) (ddcutilDisplay, bool) {
	if output == "" {
		return ddcutilDisplay{}, false
	}
	norm := func(s string) string {
		if i := strings.Index(s, "-"); strings.HasPrefix(s, "card") && i > 0 {
			s = s[i+1:]
		}
		return strings.ReplaceAll(s, "-A-", "-")
	}
	for _, d := range ds {
		if norm(d.Connector) == norm(output) {
			return d, true
		}
	}
	return ddcutilDisplay{}, false
}

// parseDdcutilVCP reads `ddcutil getvcp 10 --brief`: "VCP 10 C 50 100".
func parseDdcutilVCP(out string) (int, error) {
	cur, max, err := ddcutilFields(out)
	if err != nil {
		return 0, err
	}
	return toPercent(cur, max), nil
}

func ddcutilMax(out string) (int, error) {
	_, max, err := ddcutilFields(out)
	return max, err
}

func ddcutilFields(out string) (int, int, error) {
	f := strings.Fields(out)
	if len(f) >= 5 && f[0] == "VCP" && f[2] == "C" {
		cur, err1 := strconv.Atoi(f[3])
		max, err2 := strconv.Atoi(f[4])
		if err1 == nil && err2 == nil && max > 0 {
			return cur, max, nil
		}
	}
	return 0, 0, fmt.Errorf("unexpected ddcutil output %q", strings.TrimSpace(out))
}
