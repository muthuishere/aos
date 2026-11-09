package groups

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// With an initial notification, NotifyIpInterfaceChange calls back at once,
// which proves the callback works without touching the network.
func TestNetCallbackFires(t *testing.T) {
	var h windows.Handle
	if err := windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, netCallback, nil, true, &h); err != nil {
		t.Fatal(err)
	}
	defer windows.CancelMibChangeNotify2(h)
	select {
	case <-netChanged:
	case <-time.After(5 * time.Second):
		t.Fatal("no NotifyIpInterfaceChange callback within 5s")
	}
}
