package groups

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTMLText(t *testing.T) {
	doc := `<html><head><title>T</title><style>p{}</style></head><body>
<script>var nonce="abc123"</script><!-- build 42 -->
<h1>Release&nbsp;notes</h1><p>Version <b>2.0</b> &amp; more</p><ul><li>one</li><li>two</li></ul>
</body></html>`
	got := htmlText(doc)
	want := []string{"T", "Release notes", "Version 2.0 & more", "one", "two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func urlTestServer(pages ...string) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := int(n.Add(1)) - 1
		p := pages[min(i, len(pages)-1)]
		if p == "500" {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// The nonce changes on every load and must not count as a change.
		fmt.Fprintf(w, `<html><body><script>var n=%d</script>%s</body></html>`, i, p)
	}))
	return srv, &n
}

func runURLWatch(t *testing.T, o urlWatch) (string, error) {
	t.Helper()
	c, out := pageCtx()
	o.every = time.Millisecond
	o.jitter = func(d time.Duration) time.Duration { return d }
	o.fetch = func(ctx context.Context) (int, []string, error) { return fetchPageText(ctx, o.url) }
	err := watchURLLoop(c, o)
	return out.String(), err
}

func TestWatchURL(t *testing.T) {
	// No condition: one line per change of the text, markup churn ignored.
	srv, _ := urlTestServer("<p>a</p>", "<p>a</p>", "<p>a</p><p>b</p>", "<p>b</p>")
	defer srv.Close()
	out, err := runURLWatch(t, urlWatch{url: srv.URL, max: 2})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if err != nil || len(lines) != 2 || !strings.Contains(lines[0], `"added":["b"]`) || !strings.Contains(lines[1], `"removed":["a"]`) {
		t.Fatalf("changes: %v\n%s", err, out)
	}

	// --match: exit 0 when the text shows up; a 500 in between is not news.
	srv, n := urlTestServer("<p>wait</p>", "500", "<p>Book now</p>")
	defer srv.Close()
	out, err = runURLWatch(t, urlWatch{url: srv.URL, match: "book now"})
	if err != nil || n.Load() != 3 || !strings.Contains(out, `"match":"book now"`) {
		t.Fatalf("match: %v n=%d\n%s", err, n.Load(), out)
	}

	// --gone: a failing fetch is not "gone".
	srv, n = urlTestServer("<p>Out of stock</p>", "500", "<p>In stock</p>")
	defer srv.Close()
	out, err = runURLWatch(t, urlWatch{url: srv.URL, gone: "out of stock"})
	if err != nil || n.Load() != 3 || !strings.Contains(out, `"gone":"out of stock"`) {
		t.Fatalf("gone: %v n=%d\n%s", err, n.Load(), out)
	}

	// --timeout exits 3.
	srv, _ = urlTestServer("<p>x</p>")
	defer srv.Close()
	_, err = runURLWatch(t, urlWatch{url: srv.URL, match: "never", timeout: 20 * time.Millisecond})
	if exitCode(err) != exitNotFound {
		t.Fatalf("timeout: %v", err)
	}
}

func TestWatchURLFlags(t *testing.T) {
	c, _ := pageCtx()
	for _, args := range [][]string{
		{},
		{"ftp://x"},
		{"https://example.com", "--every=5s"},
		{"https://example.com", "--match=a", "--gone=b"},
		{"https://example.com", "--timeout=nope"},
	} {
		if err := runWatchURL(c, args); err == nil {
			t.Errorf("%q accepted", args)
		}
	}
	if j := urlJitter(time.Minute); j < time.Minute || j >= time.Minute+6*time.Second {
		t.Fatalf("jitter %s", j)
	}
}
