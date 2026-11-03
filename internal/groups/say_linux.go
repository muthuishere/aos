package groups

import "os/exec"

// aos say targets macOS and Windows; the commands are declared for those only.
const sayNativeExt = ".wav"

func sayRender(sayRequest, string) error { return errUnsupported }
func playCommand(clip string) *exec.Cmd  { return exec.Command("false") }
func sayVoices() ([]sayVoice, error)     { return nil, errUnsupported }
func killTree(int)                       {}
