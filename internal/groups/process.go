package groups

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/muthuishere/aos/internal/cli"
)

// processInfo is one running process, in the shape `process list --json` emits.
//
// CPU is a percentage of one core on macOS and Linux, where `ps` reports it
// directly. Windows has no cheap equivalent, so there it is the process's total
// CPU seconds since it started — the field is still comparable between
// processes on the same machine, which is what it is used for.
type processInfo struct {
	PID      int     `json:"pid"`
	Name     string  `json:"name"`
	CPU      float64 `json:"cpu"`
	MemoryMB float64 `json:"memory_mb"`
	User     string  `json:"user,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Describe("process", "Running processes: list, find, kill")
		r.Add(
			&cli.Command{
				Group: "process", Name: "list",
				Summary: "List running processes",
				Args:    "[--filter=<substring>] [--json]",
				Examples: []string{
					"aos process list",
					"aos process list --filter=chrome",
					"aos process list --json",
				},
				Run: runProcessList,
			},
			&cli.Command{
				Group: "process", Name: "find",
				Summary: "Find processes whose name contains a substring",
				Args:    "<name> [--json]",
				Examples: []string{
					"aos process find Chrome",
					"aos process find node --json",
				},
				Run: runProcessFind,
			},
			&cli.Command{
				Group: "process", Name: "kill",
				Summary: "Kill a process by pid, or by name when it is unambiguous",
				Args:    "<pid|name> [--force]",
				Examples: []string{
					"aos process kill 4821",
					"aos process kill Calculator",
					"aos process kill Calculator --force",
				},
				Run: runProcessKill,
			},
		)
	})
}

func runProcessList(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "filter")
	if err != nil {
		return err
	}
	if err := set.Reject("filter", "json"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`process list` takes no positional arguments; did you mean `process find %s`?", set.Rest[0])
	}

	found, err := listProcesses()
	if err != nil {
		return err
	}
	if filter := set.String("filter", ""); filter != "" {
		found = matchProcesses(found, filter)
	}
	return printProcesses(c, found, set.Has("json"))
}

func runProcessFind(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("json"); err != nil {
		return err
	}
	if len(set.Rest) == 0 {
		return fmt.Errorf("`process find` needs a name to look for")
	}
	query := strings.Join(set.Rest, " ")

	all, err := listProcesses()
	if err != nil {
		return err
	}
	matches := matchProcesses(all, query)
	if len(matches) == 0 {
		// An empty list is a real answer for `list`, but `find` was asked about
		// something specific, so a miss is a failure the caller can branch on.
		if set.Has("json") {
			if err := printProcesses(c, matches, true); err != nil {
				return err
			}
		}
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("no process matching %q", query)}
	}
	return printProcesses(c, matches, set.Has("json"))
}

func runProcessKill(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args)
	if err != nil {
		return err
	}
	if err := set.Reject("force", "f"); err != nil {
		return err
	}
	if len(set.Rest) == 0 {
		return fmt.Errorf("`process kill` needs a pid or a process name")
	}
	force := set.Has("force") || set.Has("f")
	target := strings.Join(set.Rest, " ")

	// A bare number is a pid. Anything else is a name, and has to resolve to
	// exactly one process before anything is killed.
	name := ""
	pid, err := strconv.Atoi(target)
	if err != nil {
		all, listErr := listProcesses()
		if listErr != nil {
			return listErr
		}
		chosen, resolveErr := resolveKillTarget(all, target)
		if resolveErr != nil {
			return resolveErr
		}
		pid, name = chosen.PID, chosen.Name
	}

	if err := guardKill(pid, os.Getpid()); err != nil {
		return err
	}
	if err := killProcess(pid, force); err != nil {
		return &cli.ExitError{Code: 1, Message: err.Error()}
	}
	if name != "" {
		c.Printf("killed %d  %s\n", pid, name)
		return nil
	}
	c.Printf("killed %d\n", pid)
	return nil
}

// printProcesses writes a table, or the JSON array, in the order given.
func printProcesses(c *cli.Ctx, found []processInfo, asJSON bool) error {
	if asJSON {
		if found == nil {
			found = []processInfo{}
		}
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(found)
	}
	for _, p := range found {
		c.Printf("%7d  %5.1f  %9.1fMB  %-12s %s\n", p.PID, p.CPU, p.MemoryMB, p.User, p.Name)
	}
	return nil
}

// matchProcesses keeps the processes whose name contains query, ignoring case.
// Results stay sorted by pid so two runs of the same query read the same.
func matchProcesses(all []processInfo, query string) []processInfo {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	var matches []processInfo
	for _, p := range all {
		if strings.Contains(strings.ToLower(p.Name), query) {
			matches = append(matches, p)
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].PID < matches[j].PID })
	return matches
}

// resolveKillTarget turns a name into the single process to kill.
//
// Killing the wrong process is not undoable, so an ambiguous name is refused
// with the candidates listed rather than resolved by a guess. An exact
// name match still wins over substring hits — otherwise `kill Code` could not
// name "Code" while "Code Helper" is also running.
func resolveKillTarget(all []processInfo, query string) (processInfo, error) {
	matches := matchProcesses(all, query)
	if len(matches) == 0 {
		return processInfo{}, &cli.ExitError{Code: 1, Message: fmt.Sprintf("no process matching %q", query)}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}

	var exact []processInfo
	lowered := strings.ToLower(strings.TrimSpace(query))
	for _, p := range matches {
		if strings.ToLower(p.Name) == lowered {
			exact = append(exact, p)
		}
	}
	if len(exact) == 1 {
		return exact[0], nil
	}

	candidates := exact
	if len(candidates) == 0 {
		candidates = matches
	}
	// A loose substring can match hundreds of processes, and a screen of them
	// buries the one line that matters, so show a readable sample and say how
	// many were left out.
	const shown = 10
	var lines []string
	for i, p := range candidates {
		if i == shown {
			lines = append(lines, fmt.Sprintf("  ... and %d more", len(candidates)-shown))
			break
		}
		lines = append(lines, fmt.Sprintf("  %d  %s", p.PID, p.Name))
	}
	return processInfo{}, &cli.ExitError{
		Code: 1,
		Message: fmt.Sprintf("%q matches %d processes; kill one by pid, or narrow the name:\n%s",
			query, len(candidates), strings.Join(lines, "\n")),
	}
}

// guardKill refuses the two kills that are never what a caller meant: pid 1,
// which is init/launchd and takes the machine with it, and aos itself, which
// would kill the command mid-run.
func guardKill(pid, self int) error {
	if pid < 1 {
		return fmt.Errorf("%d is not a valid pid", pid)
	}
	if pid == 1 {
		return fmt.Errorf("refusing to kill pid 1")
	}
	if pid == self {
		return fmt.Errorf("refusing to kill aos itself (pid %d)", pid)
	}
	return nil
}

// parsePsOutput reads the fixed column order this group asks `ps` for:
//
//	pid  %cpu  rss(KB)  user  command
//
// The command is last because it is the only field that can contain spaces —
// on macOS it is a full bundle path — so everything after the user column is
// one value, and the process name is its base.
func parsePsOutput(out string) []processInfo {
	var found []processInfo
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 5 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue // the header line, if a `ps` ever emits one
		}
		cpu, _ := strconv.ParseFloat(fields[1], 64)
		rssKB, _ := strconv.ParseFloat(fields[2], 64)
		name := processBase(strings.Join(fields[4:], " "))
		if name == "" {
			continue
		}
		found = append(found, processInfo{
			PID:      pid,
			Name:     name,
			CPU:      cpu,
			MemoryMB: rssKB / 1024,
			User:     fields[3],
		})
	}
	return found
}

// processBase reduces a command to the name people recognise: the last path
// element, with a kernel thread's brackets and a trailing .exe left intact so
// the name still matches what other tools show.
func processBase(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	// Windows paths arrive with backslashes even when aos is parsing them
	// elsewhere, so normalise before taking the base.
	command = strings.ReplaceAll(command, "\\", "/")
	return filepath.Base(command)
}
