package groups

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// nowPlaying is what `media now --json` emits. Position and Duration are in
// seconds, -1 when the player does not say.
type nowPlaying struct {
	App      string  `json:"app"`
	State    string  `json:"state"` // playing, paused, stopped
	Title    string  `json:"title"`
	Artist   string  `json:"artist,omitempty"`
	Album    string  `json:"album,omitempty"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
}

// mediaActions are the transport commands, in the words people use.
var mediaActions = []struct{ name, summary string }{
	{"play", "Resume playback"},
	{"pause", "Pause playback"},
	{"toggle", "Play if paused, pause if playing"},
	{"next", "Skip to the next track"},
	{"prev", "Go back to the previous track"},
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("media", "What is playing, and play / pause / skip")
		r.Add(&cli.Command{
			Group: "media", Name: "now",
			Summary: "Print the track playing now: app, title, artist, position",
			Args:    "[--json]",
			Examples: []string{
				"aos media now",
				"aos media now --json",
			},
			Run: runMediaNow,
		})
		for _, a := range mediaActions {
			action := a.name
			r.Add(&cli.Command{
				Group: "media", Name: action,
				Summary:  a.summary,
				Examples: []string{"aos media " + action},
				Run: func(c *cli.Ctx, args []string) error {
					if len(args) > 0 {
						return fmt.Errorf("`media %s` takes no arguments", action)
					}
					return mediaControl(action)
				},
			})
		}
	})
}

func runMediaNow(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("media now", args)
	if err != nil {
		return err
	}
	np, err := readNowPlaying()
	if err != nil {
		return err
	}
	if np == nil {
		if asJSON {
			_ = writeJSON(c, nil)
		}
		return &cli.ExitError{Code: 1, Message: "nothing is playing"}
	}
	if asJSON {
		return writeJSON(c, np)
	}
	line := np.Title
	if np.Artist != "" {
		line += " — " + np.Artist
	}
	c.Printf("%s  (%s, %s", line, np.App, np.State)
	if np.Position >= 0 && np.Duration > 0 {
		c.Printf(", %s / %s", clock(np.Position), clock(np.Duration))
	}
	c.Println(")")
	return nil
}

// clock renders seconds as m:ss.
func clock(s float64) string {
	n := int(s)
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}

// nowPlayingSep separates fields in the one-line answers the macOS players
// and playerctl are asked for; a tab cannot appear in a track title there.
const nowPlayingSep = "\t"

// parseNowPlayingLine reads "state\ttitle\tartist\talbum\tposition\tduration".
// Durations from Music and playerctl are seconds; Spotify's is milliseconds,
// which durationScale corrects.
func parseNowPlayingLine(app, line string, durationScale float64) *nowPlaying {
	f := strings.Split(strings.TrimRight(line, "\r\n"), nowPlayingSep)
	if len(f) < 6 || strings.TrimSpace(f[1]) == "" {
		return nil
	}
	num := func(s string) float64 {
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(s, ",", ".")), 64)
		if err != nil {
			return -1
		}
		return v
	}
	np := &nowPlaying{
		App: app, State: normalizeState(f[0]), Title: f[1], Artist: f[2], Album: f[3],
		Position: num(f[4]), Duration: num(f[5]),
	}
	if np.Duration > 0 {
		np.Duration /= durationScale
	}
	return np
}

// normalizeState maps a player's state ("Playing", "paused", ...) to one word.
func normalizeState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "playing":
		return "playing"
	case "paused":
		return "paused"
	}
	return "stopped"
}

// pickNowPlaying prefers a player that is actually playing over one that is
// merely paused, so two open players answer with the one you hear.
func pickNowPlaying(cands []*nowPlaying) *nowPlaying {
	var paused *nowPlaying
	for _, np := range cands {
		if np == nil {
			continue
		}
		if np.State == "playing" {
			return np
		}
		if paused == nil {
			paused = np
		}
	}
	return paused
}

// toggleNeeded says whether play or pause must press play/pause, given the
// known state ("" when unknown: press it, the key is the only control there).
func toggleNeeded(action, state string) bool {
	switch action {
	case "play":
		return state != "playing"
	case "pause":
		return state == "" || state == "playing"
	}
	return true
}
