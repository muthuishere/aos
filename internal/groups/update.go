package groups

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// releasesBase is where releases live; AOS_UPDATE_BASE points it at a mirror
// with the same layout (<base>/latest redirects to <base>/tag/<v>, assets at
// <base>/download/<v>/<name>).
const releasesBase = "https://github.com/muthuishere/aos/releases"

func init() {
	register(func(r *cli.Registry) {
		r.Describe("update", "Update aos itself to the latest release")
		r.Add(&cli.Command{
			Group:   "update",
			Summary: "Download the latest aos release for this OS and CPU, verify it, and replace this binary (the old one is kept as aos.old)",
			Args:    "[--version=vX.Y.Z] [--check] [--force] [--json]",
			Examples: []string{
				"aos update",
				"aos update --check",
				"aos update --version=v0.16.0",
			},
			Run: runUpdate,
		})
	})
}

type updateResult struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Updated bool   `json:"updated"`
	Binary  string `json:"binary,omitempty"`
	Old     string `json:"old,omitempty"`
	Message string `json:"message"`
}

func runUpdate(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "version")
	if err != nil {
		return err
	}
	if err := set.Reject("version", "check", "force", "json"); err != nil {
		return err
	}
	base := strings.TrimRight(c.Env("AOS_UPDATE_BASE"), "/")
	if base == "" {
		base = releasesBase
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	pinned := set.String("version", "")
	target := pinned
	if target != "" && !strings.HasPrefix(target, "v") {
		target = "v" + target
	}
	if target == "" {
		if target, err = latestTag(client, base); err != nil {
			return err
		}
	}
	res := updateResult{From: c.Version, To: target}
	newer := versionLess(c.Version, target)
	if set.Has("check") {
		if newer {
			res.Message = fmt.Sprintf("aos %s is available (this is %s); run: aos update", target, c.Version)
		} else {
			res.Message = fmt.Sprintf("aos %s is up to date (latest %s)", c.Version, target)
		}
		return reportUpdate(c, set, res)
	}
	if !newer && !set.Has("force") && pinned == "" {
		res.Message = fmt.Sprintf("aos %s is up to date (latest %s)", c.Version, target)
		return reportUpdate(c, set, res)
	}
	exe := c.Env("AOS_UPDATE_EXE") // tests point this at a scratch file
	if exe == "" {
		if exe, err = os.Executable(); err != nil {
			return fmt.Errorf("update: cannot find this binary: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	}
	bin, err := downloadRelease(client, base, target, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	old, err := replaceBinary(exe, bin)
	if err != nil {
		return err
	}
	res.Updated, res.Binary, res.Old = true, exe, old
	res.Message = fmt.Sprintf("aos updated %s -> %s (%s; previous binary kept at %s)", c.Version, target, exe, old)
	if err := reportUpdate(c, set, res); err != nil {
		return err
	}
	// Inside WSL the screen commands run through the Windows aos.exe: keep it
	// on the same release.
	if winExe, err := c.WindowsAOS(); err == nil && winExe != "" {
		c.Warnf("updating the Windows aos.exe as well: %s\n", winExe)
		cmd := exec.Command(winExe, "update", "--version="+target)
		cmd.Stdout, cmd.Stderr = c.Stderr, c.Stderr
		if err := cmd.Run(); err != nil {
			c.Warnf("the Windows aos.exe was not updated: %v (run aos.exe update in Windows)\n", err)
		}
	}
	return nil
}

func reportUpdate(c *cli.Ctx, set *argSet, res updateResult) error {
	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(res)
	}
	c.Println(res.Message)
	return nil
}

// latestTag reads the tag <base>/latest redirects to, without the GitHub API
// (and its rate limit).
func latestTag(client *http.Client, base string) (string, error) {
	nc := *client
	nc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := nc.Get(base + "/latest")
	if err != nil {
		return "", fmt.Errorf("update: cannot reach %s: %w", base, err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	i := strings.LastIndex(loc, "/tag/")
	if i < 0 {
		return "", fmt.Errorf("update: %s/latest did not name a release (HTTP %d)", base, resp.StatusCode)
	}
	return loc[i+len("/tag/"):], nil
}

// downloadRelease fetches the archive for goos/goarch, checks it against the
// release's checksums.txt, and returns the aos binary inside it.
func downloadRelease(client *http.Client, base, tag, goos, goarch string) ([]byte, error) {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	name := fmt.Sprintf("aos_%s_%s_%s%s", strings.TrimPrefix(tag, "v"), goos, goarch, ext)
	get := func(file string) ([]byte, error) {
		url := base + "/download/" + tag + "/" + file
		resp, err := client.Get(url)
		if err != nil {
			return nil, fmt.Errorf("update: download %s: %w", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("update: download %s: HTTP %d (is %s a release with a %s/%s build?)", url, resp.StatusCode, tag, goos, goarch)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	}
	sums, err := get("checksums.txt")
	if err != nil {
		return nil, err
	}
	archive, err := get(name)
	if err != nil {
		return nil, err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[1] == name {
			want = f[0]
		}
	}
	if want == "" {
		return nil, fmt.Errorf("update: checksums.txt of %s has no entry for %s", tag, name)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("update: %s does not match its checksum; nothing was changed", name)
	}
	binName := "aos"
	if goos == "windows" {
		binName = "aos.exe"
	}
	bin, err := extractBinary(archive, ext, binName)
	if err != nil {
		return nil, fmt.Errorf("update: %s: %w", name, err)
	}
	return bin, nil
}

func extractBinary(archive []byte, ext, binName string) ([]byte, error) {
	if ext == ".zip" {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == binName {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
		return nil, fmt.Errorf("no %s inside", binName)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no %s inside", binName)
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == binName {
			return io.ReadAll(tr)
		}
	}
}

// replaceBinary swaps exe for bin and keeps the previous file as exe's
// ".old" sibling (aos.old, aos.exe.old). Renaming works on a running binary
// on Windows too, where overwriting it in place does not; the new file is
// written next to exe first, so a failure leaves the current binary alone.
func replaceBinary(exe string, bin []byte) (string, error) {
	mode := os.FileMode(0o755)
	if st, err := os.Stat(exe); err == nil {
		mode = st.Mode().Perm() | 0o100
	}
	next, old := exe+".new", exe+".old"
	if err := os.WriteFile(next, bin, mode); err != nil {
		return "", fmt.Errorf("update: cannot write next to %s: %w", exe, err)
	}
	_ = os.Remove(old) // a previous update's backup; on Windows it may still be running
	if _, err := os.Stat(old); err == nil {
		old = exe + ".old-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	if err := os.Rename(exe, old); err != nil {
		os.Remove(next)
		return "", fmt.Errorf("update: cannot move the current binary aside: %w", err)
	}
	if err := os.Rename(next, exe); err != nil {
		if rerr := os.Rename(old, exe); rerr != nil {
			return "", errors.Join(fmt.Errorf("update: cannot put the new binary in place: %w", err), fmt.Errorf("restoring the old one failed too; it is at %s", old))
		}
		return "", fmt.Errorf("update: cannot put the new binary in place: %w; the old one is back", err)
	}
	return old, nil
}

// versionLess reports a < b for vX.Y.Z tags. A dev or unparsable current
// version is older than any release.
func versionLess(a, b string) bool {
	pa, oka := parseSemver(a)
	pb, okb := parseSemver(b)
	if !okb {
		return false
	}
	if !oka {
		return true
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
