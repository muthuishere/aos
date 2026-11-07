package groups

import (
	"strings"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/sys"
)

// playerctl speaks MPRIS to every player that implements it (Spotify,
// browsers, VLC, mpv with the plugin, ...).
func needPlayerctl() error {
	if !sys.Has("playerctl") {
		return &cli.ExitError{Code: 2, Message: "media control needs `playerctl` (MPRIS); install it from your package manager"}
	}
	return nil
}

func readNowPlaying() (*nowPlaying, error) {
	if err := needPlayerctl(); err != nil {
		return nil, err
	}
	// position and mpris:length are both microseconds.
	format := strings.Join([]string{"{{status}}", "{{title}}", "{{artist}}", "{{album}}", "{{position}}", "{{mpris:length}}"}, nowPlayingSep)
	out, err := sys.Output("playerctl", "metadata", "--format", format)
	if err != nil || strings.TrimSpace(out) == "" {
		return nil, nil // no player running: nothing is playing
	}
	app, _ := sys.Output("playerctl", "-l")
	name := strings.SplitN(strings.TrimSpace(app), "\n", 2)[0]
	if i := strings.Index(name, "."); i > 0 {
		name = name[:i]
	}
	np := parseNowPlayingLine(name, out, 1e6)
	if np != nil && np.Position > 0 {
		np.Position /= 1e6
	}
	return np, nil
}

func mediaControl(action string) error {
	if err := needPlayerctl(); err != nil {
		return err
	}
	verb := map[string]string{"play": "play", "pause": "pause", "toggle": "play-pause", "next": "next", "prev": "previous"}[action]
	_, err := sys.Output("playerctl", verb)
	return err
}
