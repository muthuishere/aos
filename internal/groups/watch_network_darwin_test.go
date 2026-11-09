package groups

import (
	"testing"
	"time"
)

// NWPathMonitor delivers the current path once on start, which proves the
// C block -> Go export path works without touching the network.
func TestNetNotifierDeliversInitialPath(t *testing.T) {
	ch, stop, err := startNetNotifier()
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no NWPathMonitor update within 5s")
	}
}
