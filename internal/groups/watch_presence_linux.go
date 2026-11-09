package groups

import "github.com/muthuishere/aos/internal/cli"

// Linux has no single system-wide "is the camera or microphone in use" signal
// (it differs between V4L2, PipeWire and PulseAudio), so the commands are not
// registered there; these stubs only keep the package compiling.
func readCameraUse() (deviceUse, error) {
	return deviceUse{}, &cli.ExitError{Code: 2, Message: "watch camera is not supported on Linux"}
}

func readMicUse() (deviceUse, error) {
	return deviceUse{}, &cli.ExitError{Code: 2, Message: "watch mic is not supported on Linux"}
}
