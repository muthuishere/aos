package groups

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// netChanged receives a tick per NotifyIpInterfaceChange callback. Buffered
// by one: a burst of callbacks collapses into one more read.
var netChanged = make(chan struct{}, 1)

// netCallback is created once: Windows has a fixed number of callback slots.
var netCallback = syscall.NewCallback(func(_, _, _ uintptr) uintptr {
	select {
	case netChanged <- struct{}{}:
	default:
	}
	return 0
})

// startNetNotifier registers NotifyIpInterfaceChange (IP Helper), which fires
// when an interface comes up or goes down, or its addresses or parameters
// change. That covers going online, going offline and joining another Wi-Fi
// network; the poll catches anything it does not signal.
func startNetNotifier() (<-chan struct{}, func(), error) {
	var h windows.Handle
	if err := windows.NotifyIpInterfaceChange(windows.AF_UNSPEC, netCallback, nil, false, &h); err != nil {
		return nil, func() {}, err
	}
	return netChanged, func() { _ = windows.CancelMibChangeNotify2(h) }, nil
}
