package groups

import "testing"

func TestParseNowPlayingLine(t *testing.T) {
	np := parseNowPlayingLine("Spotify", "playing\tSong A\tArtist B\tAlbum C\t61,5\t240000\n", 1000)
	if np == nil || np.State != "playing" || np.Title != "Song A" || np.Artist != "Artist B" || np.Album != "Album C" {
		t.Fatalf("got %+v", np)
	}
	if np.Position != 61.5 || np.Duration != 240 {
		t.Errorf("times = %v / %v", np.Position, np.Duration)
	}
	if parseNowPlayingLine("Music", "paused\t\t\t\t0\t0", 1) != nil {
		t.Error("a line without a title is not a track")
	}
	if parseNowPlayingLine("Music", "garbage", 1) != nil {
		t.Error("short line should be nil")
	}
	if np := parseNowPlayingLine("x", "Paused\tT\t\t\t\t", 1); np.State != "paused" || np.Position != -1 {
		t.Errorf("unknown times: %+v", np)
	}
}

func TestPickNowPlaying(t *testing.T) {
	music := &nowPlaying{App: "Music", State: "paused"}
	spot := &nowPlaying{App: "Spotify", State: "playing"}
	if got := pickNowPlaying([]*nowPlaying{music, nil, spot}); got != spot {
		t.Errorf("playing should win: %+v", got)
	}
	if got := pickNowPlaying([]*nowPlaying{nil, music}); got != music {
		t.Errorf("paused fallback: %+v", got)
	}
	if pickNowPlaying(nil) != nil {
		t.Error("empty should be nil")
	}
}

func TestToggleNeeded(t *testing.T) {
	cases := []struct {
		action, state string
		want          bool
	}{
		{"play", "playing", false},
		{"play", "paused", true},
		{"play", "", true},
		{"pause", "paused", false},
		{"pause", "playing", true},
		{"pause", "", true},
		{"toggle", "playing", true},
	}
	for _, tc := range cases {
		if got := toggleNeeded(tc.action, tc.state); got != tc.want {
			t.Errorf("toggleNeeded(%q, %q) = %v", tc.action, tc.state, got)
		}
	}
}

func TestClock(t *testing.T) {
	if got := clock(125.9); got != "2:05" {
		t.Errorf("clock = %q", got)
	}
}
