// Package skill ships the agent skill inside the binary and installs it where
// coding agents look for skills.
//
// Bundling rather than publishing separately means the skill can never describe
// a version of the CLI that is not the one installed: `go install` and
// `aos skill install` always agree.
package skill

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets
var bundled embed.FS

// Name is the directory the skill is installed as.
const Name = "aos"

// Host is one place skills are read from.
type Host struct {
	// Name identifies the agent, for reporting.
	Name string
	// Root is the skills directory.
	Root string
}

// Result reports what happened to one host.
type Result struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Action string `json:"action"` // installed, updated, removed, absent
}

// Hosts returns where the skill should go: both ~/.claude/skills and
// ~/.agents/skills, always.
//
// Installing conditionally would mean the skill is missing for whichever agent
// gets set up later, with nothing to indicate why — and a directory holding one
// small file costs nothing.
func Hosts(env func(string) string) ([]Host, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	claudeRoot := filepath.Join(home, ".claude", "skills")
	if dir := strings.TrimSpace(env("CLAUDE_CONFIG_DIR")); dir != "" {
		claudeRoot = filepath.Join(dir, "skills")
	}
	root := func(override, dir string) string {
		if value := strings.TrimSpace(env(override)); value != "" {
			return value
		}
		return filepath.Join(home, dir, "skills")
	}
	return []Host{
		{Name: "claude", Root: orDefault(env("CLAUDE_SKILLS_DIR"), claudeRoot)},
		{Name: "agents", Root: root("AGENTS_SKILLS_DIR", ".agents")},
	}, nil
}

// Install writes the skill to every host, overwriting an older copy.
func Install(env func(string) string) ([]Result, error) {
	hosts, err := Hosts(env)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, host := range hosts {
		target := filepath.Join(host.Root, Name)
		action := "installed"
		if _, err := os.Stat(target); err == nil {
			action = "updated"
		}
		if err := writeSkill(target); err != nil {
			return results, fmt.Errorf("%s: %w", host.Name, err)
		}
		results = append(results, Result{Host: host.Name, Path: target, Action: action})
	}
	return results, nil
}

// A host that never had it is reported as absent rather than failing, so
// uninstall is safe to run twice.
// Uninstall removes the skill from every host.
func Uninstall(env func(string) string) ([]Result, error) {
	hosts, err := Hosts(env)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, host := range hosts {
		target := filepath.Join(host.Root, Name)
		if _, err := os.Stat(target); err != nil {
			results = append(results, Result{Host: host.Name, Path: target, Action: "absent"})
			continue
		}
		if err := os.RemoveAll(target); err != nil {
			return results, fmt.Errorf("%s: %w", host.Name, err)
		}
		results = append(results, Result{Host: host.Name, Path: target, Action: "removed"})
	}
	return results, nil
}

// Content returns the skill as it would be installed, for `skill show`.
func Content() (string, error) {
	data, err := bundled.ReadFile(filepath.ToSlash(filepath.Join("assets", Name, "SKILL.md")))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// writeSkill copies the embedded tree to target, replacing what is there.
func writeSkill(target string) error {
	// Replace rather than merge: a stale file left behind by an older version
	// would be read by the agent as though it were current.
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	source := filepath.ToSlash(filepath.Join("assets", Name))

	if err := fs.WalkDir(bundled, source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, filepath.FromSlash(relative))
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := bundled.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o644)
	}); err != nil {
		return err
	}
	// The stamp is what lets Ensure tell a current install from a stale one.
	return os.WriteFile(filepath.Join(target, StampFile), []byte(BundleHash()+"\n"), 0o644)
}

func orDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
