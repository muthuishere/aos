package groups

// syscallUmask is a no-op on Windows, which has no umask.
func syscallUmask(mask int) int { return 0 }
