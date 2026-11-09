package groups

import (
	"strings"
	"testing"
	"time"

	"github.com/muthuishere/aos/internal/engine"
)

func TestWatchTabGoneNearAndReload(t *testing.T) {
	seq := func(reads ...[]string) func() ([]string, error) {
		i := 0
		return func() ([]string, error) {
			r := reads[min(i, len(reads)-1)]
			i++
			return r, nil
		}
	}

	// --gone exits 0 once the text is off the page; an empty read (a page
	// between loads) does not count.
	c, out := pageCtx()
	err := watchTabLoop(c, tabWatch{tab: "chrome:1:2", gone: "out of stock", interval: time.Millisecond,
		read: seq([]string{"Out of stock"}, []string{}, []string{"Out of stock"}, []string{"In stock"})})
	if err != nil || !strings.Contains(out.String(), `"gone":"out of stock"`) {
		t.Fatalf("gone: %v\n%s", err, out.String())
	}
	c, _ = pageCtx()
	err = watchTabLoop(c, tabWatch{tab: "chrome:1:2", gone: "x", interval: time.Millisecond, timeout: 20 * time.Millisecond,
		read: func() ([]string, error) { return []string{"x"}, nil }})
	if exitCode(err) != exitNotFound {
		t.Fatalf("gone timeout: %v", err)
	}

	// --near only sees lines around the anchor: "Sold out" far away is ignored.
	far := append([]string{"Price"}, strings.Split("a b c d e f g h", " ")...)
	c, out = pageCtx()
	err = watchTabLoop(c, tabWatch{tab: "chrome:1:2", match: "sold out", near: "price", interval: time.Millisecond, timeout: 30 * time.Millisecond,
		read: seq(far, append(append([]string{}, far...), "Sold out"))})
	if exitCode(err) != exitNotFound || out.Len() != 0 {
		t.Fatalf("near far: %v\n%s", err, out.String())
	}
	c, _ = pageCtx()
	err = watchTabLoop(c, tabWatch{tab: "chrome:1:2", match: "sold out", near: "price", interval: time.Millisecond,
		read: seq([]string{"Price", "$10"}, []string{"Price", "Sold out"})})
	if err != nil {
		t.Fatalf("near: %v", err)
	}

	// --reload presses reload, waits for the page to settle, then compares.
	reloads := 0
	page := []string{"No slots"}
	clock := time.Unix(0, 0)
	c, out = pageCtx()
	err = watchTabLoop(c, tabWatch{tab: "chrome:1:2", match: "Book now", interval: time.Millisecond, reload: 10 * time.Second,
		now:    func() time.Time { clock = clock.Add(time.Second); return clock },
		jitter: func(d time.Duration) time.Duration { return d },
		sleep:  func(time.Duration) {},
		reloadFn: func() error {
			reloads++
			if reloads == 2 {
				page = []string{"Book now"}
			}
			return nil
		},
		read: func() ([]string, error) { return page, nil }})
	if err != nil || reloads != 2 || !strings.Contains(out.String(), "Book now") {
		t.Fatalf("reload: %v reloads=%d\n%s", err, reloads, out.String())
	}

	if j := reloadJitter(10 * time.Second); j < 10*time.Second || j >= 11*time.Second {
		t.Fatalf("jitter %s", j)
	}
	if b, ok := reloadButton([]engine.TextItem{{Text: "Back", Role: "button"}, {Text: "Refresh", Role: "button"}}); !ok || b.Text != "Refresh" {
		t.Fatal("Edge's Refresh button not found")
	}
	if _, ok := reloadButton([]engine.TextItem{{Text: "Reload", Role: "text"}}); ok {
		t.Fatal("non-button taken for reload")
	}
}

func TestWatchTabReloadFlags(t *testing.T) {
	withFakePage(t, fakePage(), func(int, int, string, string) error { return nil })
	c, _ := pageCtx()
	if err := runWatchTab(c, []string{"--id=chrome:1:2", "--reload=5s"}); err == nil {
		t.Fatal("--reload under 10s accepted")
	}
	if err := runWatchTab(c, []string{"--id=chrome:1:2", "--match=a", "--gone=b"}); err == nil {
		t.Fatal("--match with --gone accepted")
	}
}
