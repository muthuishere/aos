//go:build !darwin && !windows

package groups

import "errors"

// startNetNotifier: no OS notification is wired up here; the watch polls.
func startNetNotifier() (<-chan struct{}, func(), error) {
	return nil, func() {}, errors.New("not wired up on this platform")
}
