package cli

import "os"

// ownedByMe has no uid to compare on Windows. Returning true is safe because
// a Windows aos never auto-detects a socket host: the SSH auto-detection that
// trusts /tmp/aos-$USER.sock only matters on the Unix server end of an SSH
// session, so a socket is only ever used here when explicitly configured.
func ownedByMe(os.FileInfo) bool { return true }
