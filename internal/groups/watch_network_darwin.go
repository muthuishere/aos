package groups

/*
#cgo CFLAGS: -fblocks
#cgo LDFLAGS: -framework Network
void aosNetStart(void);
void aosNetStop(void);
*/
import "C"

// netChanged receives a tick per NWPathMonitor update. Buffered by one: a
// burst of updates while Go is still reading collapses into one more read.
var netChanged = make(chan struct{}, 1)

//export aosNetPathChanged
func aosNetPathChanged() {
	select {
	case netChanged <- struct{}{}:
	default:
	}
}

// startNetNotifier starts NWPathMonitor. Its first update (the current path,
// delivered on start) only causes a read that finds nothing changed.
func startNetNotifier() (<-chan struct{}, func(), error) {
	C.aosNetStart()
	return netChanged, func() { C.aosNetStop() }, nil
}
