package groups

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeRelease(t *testing.T, tag, goos, goarch string, body []byte, corrupt bool) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	tw.Write([]byte("hi"))
	tw.WriteHeader(&tar.Header{Name: "aos", Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Close()
	gz.Close()
	name := fmt.Sprintf("aos_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), goos, goarch)
	sum := sha256.Sum256(buf.Bytes())
	if corrupt {
		sum[0] ^= 0xff
	}
	sums := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(sums)) })
	mux.HandleFunc("/download/"+tag+"/"+name, func(w http.ResponseWriter, _ *http.Request) { w.Write(buf.Bytes()) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateDownloadsVerifiesAndSwaps(t *testing.T) {
	srv := fakeRelease(t, "v9.9.9", "linux", "amd64", []byte("NEW"), false)
	tag, err := latestTag(srv.Client(), srv.URL)
	if err != nil || tag != "v9.9.9" {
		t.Fatalf("latestTag = %q, %v", tag, err)
	}
	bin, err := downloadRelease(srv.Client(), srv.URL, tag, "linux", "amd64")
	if err != nil || string(bin) != "NEW" {
		t.Fatalf("downloadRelease = %q, %v", bin, err)
	}
	exe := filepath.Join(t.TempDir(), "aos")
	os.WriteFile(exe, []byte("OLD"), 0o755)
	old, err := replaceBinary(exe, bin)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "NEW" {
		t.Fatalf("binary = %q", got)
	}
	if got, _ := os.ReadFile(old); string(got) != "OLD" {
		t.Fatalf("old binary = %q at %s", got, old)
	}
	if st, _ := os.Stat(exe); st.Mode().Perm()&0o100 == 0 {
		t.Fatal("new binary is not executable")
	}
}

func TestUpdateRefusesBadChecksum(t *testing.T) {
	srv := fakeRelease(t, "v9.9.9", "linux", "amd64", []byte("NEW"), true)
	if _, err := downloadRelease(srv.Client(), srv.URL, "v9.9.9", "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want checksum error, got %v", err)
	}
}

func TestUpdateMissingBuild(t *testing.T) {
	srv := fakeRelease(t, "v9.9.9", "linux", "amd64", []byte("NEW"), false)
	if _, err := downloadRelease(srv.Client(), srv.URL, "v9.9.9", "darwin", "arm64"); err == nil {
		t.Fatal("want an error for a build the release lacks")
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.16.0", "v0.17.0", true}, {"v0.17.0", "v0.17.0", false}, {"v0.17.1", "v0.17.0", false},
		{"dev", "v0.1.0", true}, {"v0.9.0", "v0.10.0", true}, {"v0.17.0-rc1", "v0.17.0", false},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}
