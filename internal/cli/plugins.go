package cli

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// metadataScanLimit matches omarchy: only the first lines of a script are read
// looking for metadata, so discovery stays cheap over hundreds of files.
const metadataScanLimit = 80

// Prefixes are the file-name prefixes an external command may carry. The
// command is `aos`, so that is what a plugin author reaches for; the longer
// project name is accepted too, since both are obvious guesses.
var Prefixes = []string{"aos-", "agentic-os-"}

// Prefix is the canonical prefix, used in documentation and examples.
var Prefix = Prefixes[0]

// metaKeys are the comment tags external commands use to describe themselves,
// e.g. `# aos:summary=Apply a theme`.
//
// Both are accepted for the same reason both file-name prefixes are: the
// documented form is the short one, and a plugin author who wrote the long one
// should not silently lose its description. Only the file name was forgiving
// before, so a plugin named `aos-demo-thing` carrying `# aos:summary=` was
// discovered and then listed with no summary at all.
var metaKeys = []string{"aos:", "agentic-os:"}

// PluginDirs lists, in order, where external commands are looked for:
// $AOS_BIN_DIR, the user config bin dir, then every PATH entry.
func PluginDirs(env func(string) string) []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" {
			return
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	add(env("AOS_BIN_DIR"))
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".config", "aos", "bin"))
	}
	for _, dir := range filepath.SplitList(env("PATH")) {
		add(dir)
	}
	return dirs
}

// DiscoverPlugins registers every external `aos-<route>` executable it
// can find. Builtins already registered win: a duplicate route is skipped, so
// shipping a Go implementation of a command shadows a script of the same name.
func DiscoverPlugins(r *Registry, env func(string) string) {
	for _, dir := range PluginDirs(env) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			stem, ok := trimPluginPrefix(name)
			if !ok {
				continue
			}
			path := filepath.Join(dir, name)
			if !isExecutable(path) {
				continue
			}
			cmd := parsePlugin(path, stem)
			if cmd == nil {
				continue
			}
			if r.Lookup(cmd.Group, cmd.Name) != nil {
				continue
			}
			r.Add(cmd)
		}
	}
}

// trimPluginPrefix removes whichever accepted prefix a file carries.
func trimPluginPrefix(name string) (string, bool) {
	for _, prefix := range Prefixes {
		if strings.HasPrefix(name, prefix) {
			return strings.TrimPrefix(name, prefix), true
		}
	}
	return "", false
}

// parsePlugin turns a file name and its metadata header into a Command.
// `aos-audio-output-volume` becomes the route `audio output volume`.
func parsePlugin(path, stem string) *Command {
	stem = strings.TrimSuffix(stem, filepath.Ext(stem))
	if stem == "" {
		return nil
	}
	tokens := strings.Split(stem, "-")
	cmd := &Command{
		Group:  tokens[0],
		Name:   strings.Join(tokens[1:], " "),
		Binary: path,
	}
	applyMetadata(cmd, readMetadata(path))
	return cmd
}

func applyMetadata(cmd *Command, meta map[string]string) {
	for key, value := range meta {
		switch key {
		case "summary":
			cmd.Summary = value
		case "args":
			cmd.Args = value
		case "examples":
			cmd.Examples = splitPipe(value)
		case "aliases":
			cmd.Aliases = splitPipe(value)
		case "platforms":
			cmd.Platforms = splitPipe(value)
		case "route":
			// An explicit route wins over the name-derived one, for commands
			// whose own words contain a hyphen (`theme bg-switcher`).
			group, name, _ := strings.Cut(strings.TrimSpace(value), " ")
			cmd.Group, cmd.Name = group, name
		case "hidden":
			cmd.Hidden = isTrue(value)
		}
	}
	if cmd.Summary == "" {
		cmd.Summary = "(no summary)"
	}
}

// readMetadata scans the head of a file for `# aos:key=value` comments.
func readMetadata(path string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	meta := map[string]string{}
	scanner := bufio.NewScanner(file)
	for line := 0; line < metadataScanLimit && scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		text = strings.TrimLeft(text, "#/;'\" \t")
		rest, ok := trimMetaKey(text)
		if !ok {
			continue
		}
		key, value, ok := strings.Cut(rest, "=")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return meta
}

// trimMetaKey removes whichever metadata tag a line carries.
func trimMetaKey(text string) (string, bool) {
	for _, key := range metaKeys {
		if strings.HasPrefix(text, key) {
			return strings.TrimPrefix(text, key), true
		}
	}
	return "", false
}

func splitPipe(value string) []string {
	var out []string
	for _, part := range strings.Split(value, "|") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func isTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".exe", ".bat", ".cmd", ".com": // never .ps1: aos starts no PowerShell
			return true
		}
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// runExternal executes a discovered plugin, passing our stdio through.
func runExternal(c *Ctx, cmd *Command, args []string) int {
	child := exec.Command(cmd.Binary, args...)
	child.Stdin, child.Stdout, child.Stderr = c.Stdin, c.Stdout, c.Stderr
	child.Env = append(os.Environ(),
		"AOS=1",
		"AOS_VERSION="+c.Version,
		"AOS_ROUTE="+cmd.Route(),
	)
	if err := child.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		c.Warnf("aos: %s: %v\n", cmd.Route(), err)
		return 1
	}
	return 0
}
