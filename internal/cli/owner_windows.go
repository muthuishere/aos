package cli

import "os"

// ownedByMe has no uid to compare on Windows; the socket path is per-user.
func ownedByMe(os.FileInfo) bool { return true }
