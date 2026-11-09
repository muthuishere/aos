package groups

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fileServerFixture builds dir/{a.txt, sub/b.txt, .secret, .git/config} and an
// outside directory, and returns a test server over dir.
func fileServerFixture(t *testing.T, token string, hidden bool) (*httptest.Server, string, string) {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "served")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{dir, outside, filepath.Join(dir, "sub"), filepath.Join(dir, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "a.txt"), "alpha")
	write(filepath.Join(dir, "sub", "b.txt"), "beta")
	write(filepath.Join(dir, ".secret"), "hidden")
	write(filepath.Join(dir, ".git", "config"), "gitcfg")
	write(filepath.Join(outside, "passwd"), "escaped")

	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	srv := httptest.NewServer(newFileServer(fileServerOptions{Root: root, Token: token, Hidden: hidden}))
	t.Cleanup(srv.Close)
	return srv, dir, outside
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestServeFilesServesAndLists(t *testing.T) {
	srv, _, _ := fileServerFixture(t, "", false)
	if code, body := get(t, srv.URL+"/a.txt"); code != 200 || body != "alpha" {
		t.Fatalf("a.txt: %d %q", code, body)
	}
	code, body := get(t, srv.URL+"/")
	if code != 200 || !strings.Contains(body, "a.txt") || !strings.Contains(body, "sub/") {
		t.Fatalf("listing: %d %q", code, body)
	}
	resp, err := http.Post(srv.URL+"/a.txt", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST: got %d, want 405", resp.StatusCode)
	}
}

func TestServeFilesTokenRequired(t *testing.T) {
	srv, _, _ := fileServerFixture(t, "tok123", false)
	for _, p := range []string{"/", "/a.txt", "/wrong/a.txt", "/tok12/a.txt", "/tok1234/a.txt"} {
		if code, _ := get(t, srv.URL+p); code != 404 {
			t.Errorf("%s without token: got %d, want 404", p, code)
		}
	}
	if code, body := get(t, srv.URL+"/tok123/a.txt"); code != 200 || body != "alpha" {
		t.Fatalf("with token: %d %q", code, body)
	}
	if code, body := get(t, srv.URL+"/tok123/sub/b.txt"); code != 200 || body != "beta" {
		t.Fatalf("nested with token: %d %q", code, body)
	}
}

func TestServeFilesPathTraversal(t *testing.T) {
	srv, _, _ := fileServerFixture(t, "", false)
	for _, p := range []string{"/../outside/passwd", "/sub/../../outside/passwd", "/%2e%2e/outside/passwd", "/..%2foutside%2fpasswd"} {
		req, _ := http.NewRequest("GET", srv.URL+"/", nil)
		req.URL.RawPath, req.URL.Path = "", "" // keep the raw form on the wire
		req.URL.Opaque = p
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "escaped") {
			t.Errorf("%s leaked a file outside the root", p)
		}
	}
}

func TestServeFilesSymlinkEscape(t *testing.T) {
	srv, dir, outside := fileServerFixture(t, "", false)
	if err := os.Symlink(filepath.Join(outside, "passwd"), filepath.Join(dir, "link.txt")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("symlinks need privilege on this Windows box")
		}
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "outdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "inside.txt")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/link.txt", "/outdir/passwd", "/outdir/"} {
		if code, body := get(t, srv.URL+p); code == 200 || strings.Contains(body, "escaped") {
			t.Errorf("%s: got %d %q, want refused", p, code, body)
		}
	}
	_, listing := get(t, srv.URL+"/")
	if strings.Contains(listing, "link.txt") || strings.Contains(listing, "outdir") {
		t.Errorf("listing shows escaping symlinks: %q", listing)
	}
	if code, body := get(t, srv.URL+"/inside.txt"); code != 200 || body != "alpha" {
		t.Errorf("symlink within root: %d %q", code, body)
	}
}

func TestServeFilesDotfiles(t *testing.T) {
	srv, _, _ := fileServerFixture(t, "", false)
	for _, p := range []string{"/.secret", "/.git/config", "/.git/"} {
		if code, _ := get(t, srv.URL+p); code != 404 {
			t.Errorf("%s: got %d, want 404", p, code)
		}
	}
	if _, listing := get(t, srv.URL+"/"); strings.Contains(listing, ".secret") || strings.Contains(listing, ".git") {
		t.Errorf("listing shows dotfiles: %q", listing)
	}

	shown, _, _ := fileServerFixture(t, "", true)
	if code, body := get(t, shown.URL+"/.secret"); code != 200 || body != "hidden" {
		t.Errorf("--hidden: %d %q", code, body)
	}
	if _, listing := get(t, shown.URL+"/"); !strings.Contains(listing, ".secret") {
		t.Errorf("--hidden listing lacks dotfile: %q", listing)
	}
}
