package cli

import "testing"

func TestWSLTranslatesTabOpenCwdOnly(t *testing.T) {
	wsl := &fakeWSL{files: map[string]bool{fakeExe: true, "/home/me/src": true}}
	c, _, _ := hostCtx(t, map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, wsl.deps())
	got, err := c.toWindowsArgs(&Command{Group: "tab", Name: "open"}, []string{"--app=WindowsTerminal", "--cwd=~/src"})
	if err != nil || got[1] != `--cwd=\\wsl.localhost\Ubuntu\home\me\src` {
		t.Fatalf("tab open: %q %v", got, err)
	}
	// --cwd is a folder on `tab open` only; on `tab focus` it is a substring.
	got, err = c.toWindowsArgs(&Command{Group: "tab", Name: "focus"}, []string{"--cwd=src"})
	if err != nil || got[0] != "--cwd=src" {
		t.Fatalf("tab focus: %q %v", got, err)
	}
}
