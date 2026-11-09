package groups

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "watch", Name: "url",
			Summary:  "Poll a URL over plain HTTP and report when its text changes, appears or goes away",
			Blocking: true,
			Args:     "<url> [--every=<1m>] [--match=<text> | --gone=<text>] [--exact] [--timeout=<duration>] [--max=<n>]",
			Examples: []string{
				"aos watch url https://example.com/status",
				`aos watch url https://example.com/release --every=5m --match="v2.0" --timeout=12h && aos open https://example.com/release`,
				`aos watch url https://shop.example.com/item --every=10m --gone="Out of stock"`,
				"# with --match / --gone: exit 0 when the condition is met, 3 on --timeout",
				"# pages that need a login or JavaScript: use `aos watch tab`",
			},
			Run: runWatchURL,
		})
	})
}

const (
	defaultURLEvery = time.Minute
	// minURLEvery keeps a watch polite to someone else's server.
	minURLEvery = 10 * time.Second
	// urlMaxBody bounds one fetch; past it the page is cut, not stalled on.
	urlMaxBody = 5 << 20
)

// urlEvent is the watch envelope (event, at, seq) for a URL.
type urlEvent struct {
	Event   string   `json:"event"`
	At      string   `json:"at"`
	Seq     int      `json:"seq"`
	URL     string   `json:"url"`
	Status  int      `json:"status"`
	Match   string   `json:"match,omitempty"`
	Gone    string   `json:"gone,omitempty"`
	Added   []string `json:"added,omitempty"`
	Removed []string `json:"removed,omitempty"`
}

// urlJitter adds up to a tenth of d, so watches started together do not
// hit a server in lockstep.
func urlJitter(d time.Duration) time.Duration {
	if d/10 <= 0 {
		return d
	}
	return d + time.Duration(rand.Int64N(int64(d/10)))
}

// linesMatch reports whether any line matches text.
func linesMatch(lines []string, text string, exact bool) bool {
	for _, l := range lines {
		if textMatches(l, text, exact) {
			return true
		}
	}
	return false
}

// urlClient is a seam for tests.
var urlClient = &http.Client{Timeout: 30 * time.Second}

type urlWatch struct {
	url     string
	match   string
	gone    string
	exact   bool
	every   time.Duration
	timeout time.Duration
	max     int
	fetch   func(ctx context.Context) (int, []string, error)
	jitter  func(time.Duration) time.Duration
	now     func() time.Time
}

func runWatchURL(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "every", "match", "gone", "timeout", "max")
	if err != nil {
		return err
	}
	if err := set.Reject("every", "match", "gone", "exact", "timeout", "max"); err != nil {
		return err
	}
	if len(set.Rest) != 1 {
		return fmt.Errorf("`watch url` takes exactly one URL")
	}
	raw := set.Rest[0]
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not an http(s) URL", raw)
	}
	every := defaultURLEvery
	if v := set.String("every", ""); v != "" {
		if every, err = time.ParseDuration(v); err != nil || every < minURLEvery {
			return fmt.Errorf("--every must be a duration of at least %s, got %q", minURLEvery, v)
		}
	}
	var timeout time.Duration
	if v := set.String("timeout", ""); v != "" {
		if timeout, err = time.ParseDuration(v); err != nil || timeout <= 0 {
			return fmt.Errorf("--timeout must be a duration like 30s or 5m, got %q", v)
		}
	}
	max, err := set.Int("max", 0)
	if err != nil || max < 0 {
		return fmt.Errorf("--max must be a number of at least 0")
	}
	match, gone := set.String("match", ""), set.String("gone", "")
	if match != "" && gone != "" {
		return fmt.Errorf("--match and --gone are exclusive")
	}
	return watchURLLoop(c, urlWatch{
		url: u.String(), match: match, gone: gone, exact: set.Has("exact"),
		every: every, timeout: timeout, max: max,
		fetch: func(ctx context.Context) (int, []string, error) { return fetchPageText(ctx, u.String()) },
	})
}

// fetchPageText GETs the URL and returns its status and its text, one line
// per block of the page.
func fetchPageText(ctx context.Context, u string) (int, []string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("User-Agent", "aos")
	resp, err := urlClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, urlMaxBody))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "html") || (ct == "" && strings.Contains(strings.ToLower(string(body[:min(len(body), 512)])), "<html")) {
		return resp.StatusCode, htmlText(string(body)), nil
	}
	return resp.StatusCode, textLines(string(body)), nil
}

var (
	htmlDrop  = regexp.MustCompile(`(?is)<!--.*?-->|<(script|style|noscript|template|svg)\b.*?</(script|style|noscript|template|svg)\s*>`)
	htmlBlock = regexp.MustCompile(`(?i)<\s*(br|/?(p|div|li|ul|ol|tr|td|th|table|h[1-6]|section|article|header|footer|nav|main|aside|form|blockquote|pre|dt|dd|option|button|label|title))\b[^>]*>`)
	htmlTag   = regexp.MustCompile(`(?s)<[^>]*>`)
)

// htmlText is the visible text of an HTML page: scripts, styles and
// comments dropped, block elements on lines of their own, entities decoded.
// Comparing on this rather than the raw bytes ignores markup churn (nonces,
// build hashes, inline JSON) that changes on every load.
func htmlText(doc string) []string {
	doc = htmlDrop.ReplaceAllString(doc, " ")
	doc = htmlBlock.ReplaceAllString(doc, "\n")
	doc = htmlTag.ReplaceAllString(doc, " ")
	return textLines(html.UnescapeString(doc))
}

// textLines splits text into trimmed, whitespace-collapsed, non-empty lines.
func textLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// watchURLLoop fetches every tick (jittered) and prints a line when the
// page's text changes. The first fetch is the baseline. --match exits 0 as
// soon as the text is on the page (at start too), --gone as soon as a
// successful fetch does not have it; both exit 3 on --timeout. A failed
// fetch or a non-2xx answer is reported and retried, never taken as "gone".
func watchURLLoop(c *cli.Ctx, o urlWatch) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if o.jitter == nil {
		o.jitter = urlJitter
	}
	if o.now == nil {
		o.now = time.Now
	}
	var deadline <-chan time.Time
	if o.timeout > 0 {
		t := time.NewTimer(o.timeout)
		defer t.Stop()
		deadline = t.C
	}
	emit := func(e urlEvent) error {
		e.Event, e.At, e.URL = "url", o.now().Format(time.RFC3339), o.url
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		c.Printf("%s\n", line)
		return nil
	}
	var prev []string
	started, seq, lastStatus := false, 0, 0
	for {
		status, cur, err := o.fetch(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case err != nil:
			c.Warnf("aos: %s: %v\n", o.url, err)
		case status < 200 || status > 299:
			if status != lastStatus {
				c.Warnf("aos: %s answered %d\n", o.url, status)
			}
		case o.match != "" && linesMatch(cur, o.match, o.exact):
			seq++
			return emit(urlEvent{Seq: seq, Status: status, Match: o.match})
		case o.gone != "" && !linesMatch(cur, o.gone, o.exact):
			seq++
			return emit(urlEvent{Seq: seq, Status: status, Gone: o.gone})
		case !started:
			started, prev = true, cur
		default:
			added, removed := addedText(prev, cur), addedText(cur, prev)
			prev = cur
			if len(added) > 0 || len(removed) > 0 {
				seq++
				if err := emit(urlEvent{Seq: seq, Status: status, Added: added, Removed: removed}); err != nil {
					return err
				}
				if o.match == "" && o.gone == "" && o.max > 0 && seq >= o.max {
					return nil
				}
			}
		}
		lastStatus = status
		wait := time.NewTimer(o.jitter(o.every))
		select {
		case <-ctx.Done():
			wait.Stop()
			return nil
		case <-deadline:
			wait.Stop()
			switch {
			case o.match != "":
				return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%q did not appear at %s within %s", o.match, o.url, o.timeout)}
			case o.gone != "":
				return &cli.ExitError{Code: exitNotFound, Message: fmt.Sprintf("%q was still at %s after %s", o.gone, o.url, o.timeout)}
			}
			return nil
		case <-wait.C:
		}
	}
}
