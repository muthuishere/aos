//go:build linux || windows

package groups

import "github.com/muthuishere/aos/internal/engine"

// On Linux and Windows a running app is a process with a top-level window,
// read from the same window list `window list` uses.

func quitCandidates() ([]quitTarget, error) {
	wins, err := engine.ListWindows(engine.Filter{})
	if err != nil {
		return nil, err
	}
	return windowOwners(wins), nil
}

func hideOthers() (int, error) {
	wins, err := engine.ListWindows(engine.Filter{})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, w := range othersThanFront(wins) {
		if err := minimizeWindow(w.ID); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// quitApp asks each of the app's windows to close, as clicking its close
// button would, so the app can offer to save.
func quitApp(t quitTarget) error {
	wins, err := engine.ListWindows(engine.Filter{})
	if err != nil {
		return err
	}
	for _, w := range wins {
		if w.PID == t.PID {
			if err := closeWindow(w.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
