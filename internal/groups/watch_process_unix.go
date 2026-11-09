//go:build !windows

package groups

// watchListProcesses is the same listing `process list` prints.
func watchListProcesses() ([]processInfo, error) { return listProcesses() }

// watchHoldProcess does nothing here: there is no way to keep another
// process's exit status once it is gone.
func watchHoldProcess(int) {}

// watchProcessExitCode is never available on macOS or Linux. wait(2) only
// reports the status of our own children, and there is no public API that
// returns the exit code of an unrelated process.
func watchProcessExitCode(int) (int, bool) { return 0, false }
