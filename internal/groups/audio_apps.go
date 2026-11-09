package groups

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// audioApp is one app's audio stream, in the shape `audio apps --json`
// emits. Volume and Muted are null where the OS gives no per-app control
// (macOS). One app can own several streams (a browser's tabs); each is a row.
type audioApp struct {
	ID      string `json:"id"` // session / sink-input / process object id
	Name    string `json:"name"`
	PID     int    `json:"pid"`
	Playing bool   `json:"playing"`
	Volume  *int   `json:"volume"`
	Muted   *bool  `json:"muted"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(
			&cli.Command{
				Group: "audio", Name: "apps",
				Summary: "List the apps with audio streams, with each app's volume and mute state",
				Args:    "[--json]",
				Examples: []string{
					"aos audio apps",
					"aos audio apps --json",
				},
				Run: runAudioApps,
			},
			&cli.Command{
				Group: "audio", Name: "app",
				Summary: "Print one app's volume and mute state, or set it (Windows, Linux)",
				Args:    "<name> [<0-100>|mute|unmute]",
				Examples: []string{
					"aos audio app spotify",
					"aos audio app chrome 30",
					"aos audio app zoom mute",
					"aos audio app zoom unmute",
				},
				Run: runAudioApp,
			},
		)
	})
}

func runAudioApps(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("audio apps", args)
	if err != nil {
		return err
	}
	apps, err := listAudioApps()
	if err != nil {
		return err
	}
	if asJSON {
		if apps == nil {
			apps = []audioApp{}
		}
		return writeJSON(c, apps)
	}
	for _, a := range apps {
		printAudioApp(c, a)
	}
	return nil
}

func printAudioApp(c *cli.Ctx, a audioApp) {
	vol, mute := "-", "-"
	if a.Volume != nil {
		vol = strconv.Itoa(*a.Volume)
	}
	if a.Muted != nil {
		mute = "unmuted"
		if *a.Muted {
			mute = "muted"
		}
	}
	state := "idle"
	if a.Playing {
		state = "playing"
	}
	c.Printf("%-24s %7d  %-7s  vol %-3s  %s\n", a.Name, a.PID, state, vol, mute)
}

func runAudioApp(c *cli.Ctx, args []string) error {
	if len(args) == 0 {
		return &cli.ExitError{Code: 2, Message: "usage: aos audio app <name> [<0-100>|mute|unmute]"}
	}
	// The last word is the action when it is one; the rest is the name, so
	// `aos audio app Microsoft Teams 20` works without quotes.
	name, action := strings.Join(args, " "), ""
	if len(args) > 1 {
		last := args[len(args)-1]
		if _, err := strconv.Atoi(last); err == nil || last == "mute" || last == "unmute" {
			name, action = strings.Join(args[:len(args)-1], " "), last
		}
	}
	apps, err := listAudioApps()
	if err != nil {
		return err
	}
	targets, err := matchAudioApps(apps, name)
	if err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	if action == "" {
		for _, a := range targets {
			printAudioApp(c, a)
		}
		return nil
	}
	volume, mute := -1, -1
	switch action {
	case "mute":
		mute = 1
	case "unmute":
		mute = 0
	default:
		n, _ := strconv.Atoi(action)
		volume = clamp(n, 0, 100)
	}
	if err := setAudioApps(targets, volume, mute); err != nil {
		return err
	}
	if volume >= 0 {
		c.Printf("%s: %d\n", targets[0].Name, volume)
	} else {
		c.Printf("%s: %s\n", targets[0].Name, action+"d")
	}
	return nil
}

// matchAudioApps resolves a typed name to the streams of one app: a pid or
// stream id, then an exact name (ignoring case), then a name containing the
// text. Every stream of the matched app is returned, since a browser owns
// several. A substring that hits two different apps is refused.
func matchAudioApps(all []audioApp, query string) ([]audioApp, error) {
	q := strings.ToLower(strings.TrimSpace(query))
	var byID, exact, partial []audioApp
	for _, a := range all {
		name := strings.ToLower(a.Name)
		switch {
		case a.ID == query || strconv.Itoa(a.PID) == query:
			byID = append(byID, a)
		case name == q:
			exact = append(exact, a)
		case strings.Contains(name, q):
			partial = append(partial, a)
		}
	}
	for _, set := range [][]audioApp{byID, exact} {
		if len(set) > 0 {
			return set, nil
		}
	}
	if len(partial) == 0 {
		return nil, fmt.Errorf("no app with audio matching %q; see `aos audio apps`", query)
	}
	names := map[string]bool{}
	for _, a := range partial {
		names[a.Name] = true
	}
	if len(names) > 1 {
		list := make([]string, 0, len(names))
		for n := range names {
			list = append(list, "  "+n)
		}
		sort.Strings(list)
		return nil, fmt.Errorf("%q matches %d apps; be more specific:\n%s", query, len(list), strings.Join(list, "\n"))
	}
	return partial, nil
}

// parsePactlSinkInputs reads `pactl list sink-inputs`: one row per stream,
// with the first channel's volume and the mute flag.
func parsePactlSinkInputs(text string) []audioApp {
	var out []audioApp
	var cur *audioApp
	var binary string
	flush := func() {
		if cur != nil {
			if cur.Name == "" {
				cur.Name = binary
			}
			if cur.Name == "" {
				cur.Name = "sink-input " + cur.ID
			}
			out = append(out, *cur)
		}
		cur, binary = nil, ""
	}
	prop := func(t, key string) (string, bool) {
		v, ok := strings.CutPrefix(t, key+" = ")
		return strings.Trim(v, `"`), ok
	}
	for _, line := range strings.Split(text, "\n") {
		if id, ok := strings.CutPrefix(line, "Sink Input #"); ok {
			flush()
			cur = &audioApp{ID: strings.TrimSpace(id), Playing: true}
			continue
		}
		if cur == nil {
			continue
		}
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "Corked: "):
			cur.Playing = strings.TrimPrefix(t, "Corked: ") != "yes"
		case strings.HasPrefix(t, "Mute: "):
			m := strings.TrimPrefix(t, "Mute: ") == "yes"
			cur.Muted = &m
		case strings.HasPrefix(t, "Volume: ") && cur.Volume == nil:
			for _, f := range strings.Fields(t) {
				if n, err := strconv.Atoi(strings.TrimSuffix(f, "%")); err == nil && strings.HasSuffix(f, "%") {
					cur.Volume = &n
					break
				}
			}
		default:
			if v, ok := prop(t, "application.name"); ok {
				cur.Name = v
			} else if v, ok := prop(t, "application.process.binary"); ok {
				binary = v
			} else if v, ok := prop(t, "application.process.id"); ok {
				cur.PID, _ = strconv.Atoi(v)
			}
		}
	}
	flush()
	return out
}
