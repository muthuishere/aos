package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// StampFile sits inside an installed skill and holds the hash of the bundle it
// was copied from, so Ensure can tell current from stale without diffing.
const StampFile = ".aos-bundle"

// AutoInstallEnv disables the automatic install when set to anything.
const AutoInstallEnv = "AOS_NO_SKILL_AUTOINSTALL"

// Seams for tests, so they never touch the real home directory or PATH.
var (
	homeDir   = os.UserHomeDir
	configDir = os.UserConfigDir
	lookPath  = exec.LookPath
)

var (
	hashOnce sync.Once
	hash     string
)

// BundleHash is the sha256 over every embedded skill file (path + contents).
func BundleHash() string {
	hashOnce.Do(func() {
		h := sha256.New()
		_ = fs.WalkDir(bundled, "assets", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := bundled.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write([]byte(path))
			h.Write([]byte{0})
			h.Write(data)
			return nil
		})
		hash = hex.EncodeToString(h.Sum(nil))
	})
	return hash
}

// optOutPath is the marker `skill uninstall` leaves so the automatic install
// does not put back a skill the user removed.
func optOutPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aos", "skill-autoinstall-off"), nil
}

// SetAutoInstall records the user's choice: false after `skill uninstall`,
// true after `skill install`.
func SetAutoInstall(on bool) {
	path, err := optOutPath()
	if err != nil {
		return
	}
	if on {
		_ = os.Remove(path)
		return
	}
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		_ = os.WriteFile(path, []byte("removed by `aos skill uninstall`; `aos skill install` re-enables\n"), 0o644)
	}
}

// Ensure installs or refreshes the bundled skill when it is missing or older
// than this binary. It writes to the Claude skills root always, and to the
// agents root only when ~/.agents exists (or AGENTS_SKILLS_DIR is set) or codex
// is on PATH. It leaves alone a current install, a symlinked dev install, and
// everything when the user opted out. Only installs and updates are returned;
// callers treat errors as non-fatal.
func Ensure(env func(string) string) ([]Result, error) {
	if strings.TrimSpace(env(AutoInstallEnv)) != "" {
		return nil, nil
	}
	if path, err := optOutPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			return nil, nil
		}
	}
	hosts, err := Hosts(env)
	if err != nil {
		return nil, err
	}
	want := BundleHash()
	var out []Result
	var errs []error
	for _, host := range hosts {
		if host.Name == "agents" && !wantAgents(env) {
			continue
		}
		target := filepath.Join(host.Root, Name)
		info, statErr := os.Lstat(target)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if got, err := os.ReadFile(filepath.Join(target, StampFile)); err == nil && strings.TrimSpace(string(got)) == want {
			continue
		}
		action := "installed"
		if statErr == nil {
			action = "updated"
		}
		if err := writeSkill(target); err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, Result{Host: host.Name, Path: target, Action: action})
	}
	return out, errors.Join(errs...)
}

func wantAgents(env func(string) string) bool {
	if strings.TrimSpace(env("AGENTS_SKILLS_DIR")) != "" {
		return true
	}
	if _, err := lookPath("codex"); err == nil {
		return true
	}
	home, err := homeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".agents"))
	return err == nil
}
