package skill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// sandbox points every seam at temp dirs so nothing touches the real home.
func sandbox(t *testing.T, codex bool) (home string) {
	t.Helper()
	home = t.TempDir()
	cfg := t.TempDir()
	oldHome, oldCfg, oldLook := homeDir, configDir, lookPath
	homeDir = func() (string, error) { return home, nil }
	configDir = func() (string, error) { return cfg, nil }
	lookPath = func(string) (string, error) {
		if codex {
			return "/bin/codex", nil
		}
		return "", errors.New("not found")
	}
	t.Cleanup(func() { homeDir, configDir, lookPath = oldHome, oldCfg, oldLook })
	return home
}

func noEnv(string) string { return "" }

func TestEnsureInstallsThenIsANoOp(t *testing.T) {
	home := sandbox(t, false)
	got, err := Ensure(noEnv)
	if err != nil || len(got) != 1 || got[0].Host != "claude" || got[0].Action != "installed" {
		t.Fatalf("first ensure = %+v, %v; want claude installed only (no ~/.agents, no codex)", got, err)
	}
	stamp, err := os.ReadFile(filepath.Join(home, ".claude", "skills", Name, StampFile))
	if err != nil || len(stamp) == 0 {
		t.Fatalf("stamp missing: %v", err)
	}
	if got, _ := Ensure(noEnv); len(got) != 0 {
		t.Fatalf("second ensure changed something: %+v", got)
	}
}

func TestEnsureRefreshesAStaleInstall(t *testing.T) {
	home := sandbox(t, false)
	if _, err := Ensure(noEnv); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, ".claude", "skills", Name)
	os.WriteFile(filepath.Join(target, StampFile), []byte("old\n"), 0o644)
	os.WriteFile(filepath.Join(target, "leftover.md"), []byte("x"), 0o644)
	got, _ := Ensure(noEnv)
	if len(got) != 1 || got[0].Action != "updated" {
		t.Fatalf("want updated, got %+v", got)
	}
	if _, err := os.Stat(filepath.Join(target, "leftover.md")); err == nil {
		t.Fatal("stale file survived the refresh")
	}
}

func TestEnsureHonoursClaudeConfigDirAndAgents(t *testing.T) {
	home := sandbox(t, false)
	os.MkdirAll(filepath.Join(home, ".agents"), 0o755)
	cfg := t.TempDir()
	env := func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return cfg
		}
		return ""
	}
	got, _ := Ensure(env)
	if len(got) != 2 {
		t.Fatalf("want claude + agents, got %+v", got)
	}
	if got[0].Path != filepath.Join(cfg, "skills", Name) {
		t.Errorf("claude path = %s", got[0].Path)
	}
}

func TestEnsureUsesAgentsWhenCodexIsOnPath(t *testing.T) {
	sandbox(t, true)
	if got, _ := Ensure(noEnv); len(got) != 2 {
		t.Fatalf("want 2 hosts with codex on PATH, got %+v", got)
	}
}

func TestEnsureSkipsSymlinkedDevInstall(t *testing.T) {
	home := sandbox(t, false)
	root := filepath.Join(home, ".claude", "skills")
	os.MkdirAll(root, 0o755)
	dev := t.TempDir()
	if err := os.Symlink(dev, filepath.Join(root, Name)); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if got, _ := Ensure(noEnv); len(got) != 0 {
		t.Fatalf("symlinked install was touched: %+v", got)
	}
	if entries, _ := os.ReadDir(dev); len(entries) != 0 {
		t.Fatal("wrote through the symlink")
	}
}

func TestEnsureOptOuts(t *testing.T) {
	sandbox(t, false)
	env := func(k string) string {
		if k == AutoInstallEnv {
			return "1"
		}
		return ""
	}
	if got, _ := Ensure(env); len(got) != 0 {
		t.Fatalf("env opt-out ignored: %+v", got)
	}
	SetAutoInstall(false)
	if got, _ := Ensure(noEnv); len(got) != 0 {
		t.Fatalf("uninstall marker ignored: %+v", got)
	}
	SetAutoInstall(true)
	if got, _ := Ensure(noEnv); len(got) != 1 {
		t.Fatalf("re-enabled ensure did nothing: %+v", got)
	}
}
