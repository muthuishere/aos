package engine

import (
	"net/http"
	"strings"
	"testing"
)

// The key is the ONLY thing gating desktop control, so a too-short one
// must be refused rather than quietly accepted — and refused as an
// error, since the library never exits the caller's process.
func TestRemoteRejectsShortKey(t *testing.T) {
	h, err := Remote(RemoteOptions{Key: "short", Bind: "127.0.0.1"})
	if err == nil {
		_ = h.Stop()
		t.Fatal("expected an error for a 5-character key")
	}
	if !strings.Contains(err.Error(), "16 characters") {
		t.Fatalf("error should say why: %v", err)
	}
}

func TestRemoteToken(t *testing.T) {
	a, b := RemoteToken(), RemoteToken()
	if len(a) != 32 || a == b {
		t.Fatalf("want two distinct 32-char tokens, got %q and %q", a, b)
	}
}

// Remote returns once listening (it does not block), reports the port
// it actually bound, and Stop is idempotent.
func TestRemoteListensAndStopsIdempotently(t *testing.T) {
	h, err := Remote(RemoteOptions{Bind: "127.0.0.1"})
	if err != nil {
		t.Fatalf("Remote: %v", err)
	}
	if h.Port == 0 || !strings.Contains(h.LoopbackURL, h.Key) {
		t.Fatalf("bad handle: %+v", h)
	}
	resp, err := http.Get(h.LoopbackURL[:strings.Index(h.LoopbackURL, "/?")] + "/monitors")
	if err != nil {
		t.Fatalf("server not listening: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("untokened request should be forbidden, got %d", resp.StatusCode)
	}
	if err := h.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := h.Stop(); err != nil {
		t.Fatalf("second Stop should be a no-op, got %v", err)
	}
}
