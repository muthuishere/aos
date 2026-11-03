package groups

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
	"github.com/muthuishere/aos/internal/obs"
)

func init() {
	register(func(r *cli.Registry) {
		r.Describe("obs", "What this machine has been asked to do")
		r.Add(
			&cli.Command{
				Group: "obs", Name: "stats",
				Summary: "Summarise recorded work: calls, failures, and latency per route",
				Args:    "[--since=<duration>] [--source=<cli|mcp>] [--json]",
				Examples: []string{
					"aos obs stats",
					"aos obs stats --since=24h --source=mcp",
				},
				Run: runObsStats,
			},
			&cli.Command{
				Group: "obs", Name: "tail",
				Summary:  "Print the most recent spans, newest last",
				Args:     "[--limit=<n>] [--since=<duration>]",
				Examples: []string{"aos obs tail --limit=20"},
				Run:      runObsTail,
			},
			&cli.Command{
				Group: "obs", Name: "export",
				Summary:  "Emit recorded spans as OTLP JSON, ready for a collector",
				Args:     "[--since=<duration>]",
				Examples: []string{"aos obs export --since=1h > spans.json"},
				Run:      runObsExport,
			},
			&cli.Command{
				Group: "obs", Name: "audit",
				Summary: "Every command this machine was asked to run, oldest first",
				Args:    "[--since=<duration>] [--source=<cli|mcp>] [--failed] [--json]",
				Examples: []string{
					"aos obs audit --since=24h",
					"aos obs audit --source=mcp     # only what agents asked for",
					"aos obs audit --failed",
				},
				Run: runObsAudit,
			},
			&cli.Command{
				Group: "obs", Name: "summary",
				Summary:  "How much this machine is actually used, and by what",
				Args:     "[--since=<duration>] [--json]",
				Examples: []string{"aos obs summary", "aos obs summary --since=7d"},
				Run:      runObsSummary,
			},
			&cli.Command{
				Group: "obs", Name: "focus",
				Summary: "Who took (or tried to take) focus or the pointer through aos, newest last",
				Args:    "[--limit=<n>] [--since=<duration>] [--json]",
				Examples: []string{
					"aos obs focus --since=1h",
					"# columns: time, action, refused or done, the person's idle time, caller (agent via parent process)",
				},
				Run: runObsFocus,
			},
			&cli.Command{
				Group: "obs", Name: "path",
				Summary:  "Print where telemetry is written",
				Examples: []string{"aos obs path"},
				Run: func(c *cli.Ctx, _ []string) error {
					if obs.Disabled(c.Env) {
						c.Println("telemetry is off (AOS_TELEMETRY)")
						return &cli.ExitError{Code: 1}
					}
					c.Println(obs.Dir(c.Env))
					return nil
				},
			},
		)
	})
}

// loadSpans reads every span file in the telemetry directory, newest last,
// keeping only those inside the window and matching the source filter.
func loadSpans(c *cli.Ctx, since time.Duration, source string) ([]obs.Span, error) {
	dir := obs.Dir(c.Env)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "spans-") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names) // date-stamped, so lexical order is chronological

	cutoff := int64(0)
	if since > 0 {
		cutoff = time.Now().Add(-since).UnixNano()
	}

	var spans []obs.Span
	for _, name := range names {
		file, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		// A span line can be long; the default 64KB token limit is generous
		// but explicit here so a big line is skipped rather than truncated.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			var span obs.Span
			if err := json.Unmarshal(scanner.Bytes(), &span); err != nil {
				continue
			}
			if span.StartNanos < cutoff {
				continue
			}
			if source != "" && attrString(span, "agentic_os.source") != source {
				continue
			}
			spans = append(spans, span)
		}
		file.Close()
	}
	return spans, nil
}

func attrString(span obs.Span, key string) string {
	for _, attr := range span.Attributes {
		if attr.Key == key && attr.Value.String != nil {
			return *attr.Value.String
		}
	}
	return ""
}

func attrInt(span obs.Span, key string) int64 {
	for _, attr := range span.Attributes {
		if attr.Key == key && attr.Value.Int != nil {
			return *attr.Value.Int
		}
	}
	return 0
}

// routeStat is the per-route rollup `obs stats` prints. Metrics are derived
// from spans rather than counted separately, so the two can never disagree.
type routeStat struct {
	Route     string  `json:"route"`
	Calls     int     `json:"calls"`
	Errors    int     `json:"errors"`
	P50Millis int64   `json:"p50_ms"`
	P95Millis int64   `json:"p95_ms"`
	MaxMillis int64   `json:"max_ms"`
	ErrorRate float64 `json:"error_rate"`

	durations []int64
}

func parseSince(set *argSet) (time.Duration, error) {
	value := set.String("since", "")
	if value == "" {
		return 0, nil
	}
	since, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("--since wants a duration like 30m or 24h, got %q", value)
	}
	return since, nil
}

func runObsStats(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "since", "source")
	if err != nil {
		return err
	}
	if err := set.Reject("since", "source", "json"); err != nil {
		return err
	}
	since, err := parseSince(set)
	if err != nil {
		return err
	}

	spans, err := loadSpans(c, since, set.String("source", ""))
	if err != nil {
		return err
	}
	if len(spans) == 0 {
		c.Println("no recorded work in this window")
		return nil
	}

	byRoute := map[string]*routeStat{}
	for _, span := range spans {
		route := attrString(span, "agentic_os.route")
		if route == "" {
			route = span.Name
		}
		stat := byRoute[route]
		if stat == nil {
			stat = &routeStat{Route: route}
			byRoute[route] = stat
		}
		stat.Calls++
		if span.Status.Code == obs.StatusError {
			stat.Errors++
		}
		stat.durations = append(stat.durations, attrInt(span, "agentic_os.duration_ms"))
	}

	stats := make([]*routeStat, 0, len(byRoute))
	for _, stat := range byRoute {
		sort.Slice(stat.durations, func(i, j int) bool { return stat.durations[i] < stat.durations[j] })
		stat.P50Millis = percentile(stat.durations, 50)
		stat.P95Millis = percentile(stat.durations, 95)
		stat.MaxMillis = stat.durations[len(stat.durations)-1]
		stat.ErrorRate = float64(stat.Errors) / float64(stat.Calls)
		stats = append(stats, stat)
	}
	// Busiest first: that is what someone scanning this actually wants.
	sort.Slice(stats, func(i, j int) bool {
		if stats[i].Calls != stats[j].Calls {
			return stats[i].Calls > stats[j].Calls
		}
		return stats[i].Route < stats[j].Route
	})

	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(stats)
	}

	width := len("route")
	for _, stat := range stats {
		if len(stat.Route) > width {
			width = len(stat.Route)
		}
	}
	c.Printf("%-*s %7s %7s %8s %8s %8s\n", width, "route", "calls", "errors", "p50ms", "p95ms", "maxms")
	for _, stat := range stats {
		c.Printf("%-*s %7d %7d %8d %8d %8d\n",
			width, stat.Route, stat.Calls, stat.Errors, stat.P50Millis, stat.P95Millis, stat.MaxMillis)
	}

	totalErrors := 0
	for _, stat := range stats {
		totalErrors += stat.Errors
	}
	c.Printf("\n%d calls across %d routes, %d failed\n", len(spans), len(stats), totalErrors)
	return nil
}

// percentile picks the nearest-rank value from a sorted slice.
func percentile(sorted []int64, p int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	index := (p*len(sorted) + 99) / 100
	if index < 1 {
		index = 1
	}
	if index > len(sorted) {
		index = len(sorted)
	}
	return sorted[index-1]
}

func runObsTail(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "limit", "since")
	if err != nil {
		return err
	}
	if err := set.Reject("limit", "since"); err != nil {
		return err
	}
	limit, err := set.Int("limit", 20)
	if err != nil {
		return err
	}
	since, err := parseSince(set)
	if err != nil {
		return err
	}

	spans, err := loadSpans(c, since, "")
	if err != nil {
		return err
	}
	if len(spans) == 0 {
		c.Println("no recorded work in this window")
		return nil
	}
	if len(spans) > limit {
		spans = spans[len(spans)-limit:]
	}

	for _, span := range spans {
		mark := "ok "
		if span.Status.Code == obs.StatusError {
			mark = "err"
		}
		c.Printf("%s  %s  %-6s %6dms  %s\n",
			time.Unix(0, span.StartNanos).Format("15:04:05"),
			mark,
			attrString(span, "agentic_os.source"),
			attrInt(span, "agentic_os.duration_ms"),
			attrString(span, "agentic_os.route"))
	}
	return nil
}

// runObsExport assembles the stored lines into the OTLP JSON a collector
// accepts. The lines are already OTLP-shaped, so this only adds the envelope.
func runObsExport(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "since")
	if err != nil {
		return err
	}
	if err := set.Reject("since"); err != nil {
		return err
	}
	since, err := parseSince(set)
	if err != nil {
		return err
	}

	spans, err := loadSpans(c, since, "")
	if err != nil {
		return err
	}

	// Spans share one resource per host, so a single resourceSpans entry is
	// correct and keeps the payload small.
	var resource []obs.Attribute
	if len(spans) > 0 {
		resource = spans[0].Resource
	}
	stripped := make([]obs.Span, 0, len(spans))
	for _, span := range spans {
		span.Resource = nil
		stripped = append(stripped, span)
	}

	payload := map[string]any{
		"resourceSpans": []any{map[string]any{
			"resource": map[string]any{"attributes": resource},
			"scopeSpans": []any{map[string]any{
				"scope": map[string]any{"name": "aos", "version": c.Version},
				"spans": stripped,
			}},
		}},
	}
	enc := json.NewEncoder(c.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// runObsAudit prints the trail in the order things happened.
//
// This is the answer to "what did an agent do on my machine". `obs stats`
// aggregates and `obs tail` shows the last few; an audit needs the whole window,
// oldest first, with the timestamp a person can correlate against something else.
func runObsAudit(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "since", "source")
	if err != nil {
		return err
	}
	if err := set.Reject("since", "source", "failed", "json"); err != nil {
		return err
	}
	since, err := parseSince(set)
	if err != nil {
		return err
	}

	spans, err := loadSpans(c, since, set.String("source", ""))
	if err != nil {
		return err
	}
	if set.Has("failed") {
		var failures []obs.Span
		for _, span := range spans {
			if span.Status.Code == obs.StatusError {
				failures = append(failures, span)
			}
		}
		spans = failures
	}
	if len(spans) == 0 {
		c.Println("nothing recorded in this window")
		return nil
	}

	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(spans)
	}

	for _, span := range spans {
		result := "ok"
		if span.Status.Code == obs.StatusError {
			result = "FAILED exit " + strconv.FormatInt(attrInt(span, "agentic_os.exit_code"), 10)
		}
		c.Printf("%s  %-4s %-28s %6dms  %s\n",
			time.Unix(0, span.StartNanos).Format("2006-01-02 15:04:05"),
			attrString(span, "agentic_os.source"),
			attrString(span, "agentic_os.route"),
			attrInt(span, "agentic_os.duration_ms"),
			result)
	}
	c.Warnf("%d entries\n", len(spans))
	return nil
}

// usage is the adoption picture `obs summary` answers: is this earning its place,
// and is anything actually driving it as an agent rather than by hand.
type usage struct {
	Calls          int            `json:"calls"`
	Failed         int            `json:"failed"`
	DistinctRoutes int            `json:"distinct_routes"`
	BySource       map[string]int `json:"by_source"`
	TopRoutes      []routeCount   `json:"top_routes"`
	First          string         `json:"first,omitempty"`
	Last           string         `json:"last,omitempty"`
	DaysActive     int            `json:"days_active"`
}

type routeCount struct {
	Route string `json:"route"`
	Calls int    `json:"calls"`
}

func runObsSummary(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "since")
	if err != nil {
		return err
	}
	if err := set.Reject("since", "json"); err != nil {
		return err
	}
	since, err := parseSince(set)
	if err != nil {
		return err
	}

	spans, err := loadSpans(c, since, "")
	if err != nil {
		return err
	}
	if len(spans) == 0 {
		c.Println("nothing recorded in this window")
		return nil
	}

	report := usage{BySource: map[string]int{}}
	routes := map[string]int{}
	days := map[string]bool{}
	for _, span := range spans {
		report.Calls++
		if span.Status.Code == obs.StatusError {
			report.Failed++
		}
		source := attrString(span, "agentic_os.source")
		if source == "" {
			source = "cli"
		}
		report.BySource[source]++
		routes[attrString(span, "agentic_os.route")]++
		days[time.Unix(0, span.StartNanos).Format("2006-01-02")] = true
	}
	report.DistinctRoutes = len(routes)
	report.DaysActive = len(days)
	report.First = time.Unix(0, spans[0].StartNanos).Format(time.RFC3339)
	report.Last = time.Unix(0, spans[len(spans)-1].StartNanos).Format(time.RFC3339)

	for route, count := range routes {
		report.TopRoutes = append(report.TopRoutes, routeCount{route, count})
	}
	sort.Slice(report.TopRoutes, func(i, j int) bool {
		if report.TopRoutes[i].Calls != report.TopRoutes[j].Calls {
			return report.TopRoutes[i].Calls > report.TopRoutes[j].Calls
		}
		return report.TopRoutes[i].Route < report.TopRoutes[j].Route
	})
	if len(report.TopRoutes) > 10 {
		report.TopRoutes = report.TopRoutes[:10]
	}

	if set.Has("json") {
		enc := json.NewEncoder(c.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	c.Printf("calls        %d across %d routes, %d failed\n",
		report.Calls, report.DistinctRoutes, report.Failed)
	c.Printf("period       %s to %s (%d days with activity)\n",
		report.First[:10], report.Last[:10], report.DaysActive)
	for _, source := range []string{"cli", "mcp"} {
		if count := report.BySource[source]; count > 0 {
			c.Printf("%-12s %d\n", "by "+source, count)
		}
	}
	c.Println()
	c.Println("most used:")
	for _, route := range report.TopRoutes {
		c.Printf("  %-28s %d\n", route.Route, route.Calls)
	}
	return nil
}

// runObsFocus lists the invocations that took focus or moved the pointer
// (or were refused for it), with the caller that asked. It covers what went
// through aos; focus changes made by other programs show up in
// `aos watch window` with the app that gained focus.
func runObsFocus(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "limit", "since")
	if err != nil {
		return err
	}
	if err := set.Reject("limit", "since", "json"); err != nil {
		return err
	}
	limit, err := set.Int("limit", 50)
	if err != nil {
		return err
	}
	since, err := parseSince(set)
	if err != nil {
		return err
	}
	spans, err := loadSpans(c, since, "")
	if err != nil {
		return err
	}
	type row struct {
		Time    string `json:"time"`
		Route   string `json:"route"`
		Action  string `json:"action"`
		Refused bool   `json:"refused"`
		IdleMS  int64  `json:"user_idle_ms"`
		Caller  string `json:"caller"`
		Exit    int64  `json:"exit"`
	}
	var rows []row
	for _, span := range spans {
		action := attrString(span, "agentic_os.focus.action")
		if action == "" {
			continue
		}
		refused := false
		for _, a := range span.Attributes {
			if a.Key == "agentic_os.focus.refused" && a.Value.Bool != nil {
				refused = *a.Value.Bool
			}
		}
		rows = append(rows, row{
			Time:    time.Unix(0, span.StartNanos).Format(time.RFC3339),
			Route:   attrString(span, "agentic_os.route"),
			Action:  action,
			Refused: refused,
			IdleMS:  attrInt(span, "agentic_os.focus.user_idle_ms"),
			Caller:  attrString(span, "agentic_os.focus.caller"),
			Exit:    attrInt(span, "agentic_os.exit_code"),
		})
	}
	if len(rows) > limit {
		rows = rows[len(rows)-limit:]
	}
	if set.Has("json") {
		if rows == nil {
			rows = []row{}
		}
		return json.NewEncoder(c.Stdout).Encode(rows)
	}
	if len(rows) == 0 {
		c.Println("nothing took focus through aos in this window")
		return nil
	}
	for _, r := range rows {
		mark := "done   "
		if r.Refused {
			mark = "REFUSED"
		}
		c.Printf("%s  %s  %-32s idle %5.1fs  %s\n", r.Time[11:19], mark, r.Action, float64(r.IdleMS)/1000, r.Caller)
	}
	return nil
}
