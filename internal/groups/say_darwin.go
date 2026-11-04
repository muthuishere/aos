package groups

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/sys"
)

// sayNativeExt is what the system voice writes without conversion.
const sayNativeExt = ".aiff"

// sayRender writes one utterance with /usr/bin/say. Text goes on stdin-free
// argv after "--", so a line starting with "-" is never read as a flag.
func sayRender(req sayRequest, out string) error {
	args := []string{"-o", out}
	if req.Voice != "" {
		args = append(args, "-v", req.Voice)
	}
	if req.Rate > 0 {
		args = append(args, "-r", strconv.Itoa(req.Rate))
	}
	args = append(args, "--", req.Text)
	if msg, err := exec.Command("say", args...).CombinedOutput(); err != nil {
		m := strings.TrimSpace(string(msg))
		if strings.Contains(m, "not found") || strings.Contains(m, "Voice") {
			return fmt.Errorf("say: %s (list voices with `aos say voices`)", m)
		}
		return fmt.Errorf("say: %v: %s", err, m)
	}
	return nil
}

// playClip plays a clip with afplay and blocks until it ends, recording the
// player's PID so `say stop` can silence it.
func playClip(dir, clip string) error {
	cmd := exec.Command("afplay", clip)
	if err := cmd.Start(); err != nil {
		return err
	}
	recordPlayer(dir, cmd.Process.Pid)
	return cmd.Wait()
}

func sayVoices() ([]sayVoice, error) {
	out, err := sys.Output("say", "-v", "?")
	if err != nil {
		return nil, err
	}
	return parseMacVoices(out), nil
}

func killTree(pid int) {
	_ = exec.Command("kill", "-TERM", "-"+strconv.Itoa(pid)).Run()
	_ = exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run()
}
