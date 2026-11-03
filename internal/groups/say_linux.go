package groups

// aos say targets macOS and Windows; the commands are declared for those only.
const sayNativeExt = ".wav"

func sayRender(sayRequest, string) error { return errUnsupported }
func sayVoices() ([]sayVoice, error)     { return nil, errUnsupported }
func killTree(int)                       {}
func playClip(string, string) error      { return errUnsupported }
