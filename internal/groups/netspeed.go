package groups

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// speedBaseURL is Cloudflare's speed-test backend, the one speed.cloudflare.com
// itself uses: GET __down?bytes=N streams N bytes, POST __up swallows a body.
// No key, no account. A var so tests can point it at an httptest server.
var speedBaseURL = "https://speed.cloudflare.com"

const (
	speedPings      = 8               // tiny requests for latency
	speedDefaultMB  = 5               // download budget per run
	speedMaxMB      = 1000            // refuse anything sillier than 1 GB
	speedWarmBytes  = 100_000         // first download: warms the connection, not counted
	speedStepBytes  = 1_000_000       // second download
	speedUpWarm     = 100_000         // first upload: not counted
	speedUpFraction = 0.4             // upload budget = 40% of the download budget (2 MB at 5)
	speedTimeout    = 2 * time.Minute // whole run
)

// speedResult is what `network speed --json` emits.
type speedResult struct {
	PingMs        float64 `json:"ping_ms"`
	JitterMs      float64 `json:"jitter_ms"`
	DownloadMbps  float64 `json:"download_mbps"`
	UploadMbps    float64 `json:"upload_mbps"`
	DownloadBytes int64   `json:"download_bytes"`
	UploadBytes   int64   `json:"upload_bytes"`
	Colo          string  `json:"colo,omitempty"` // the Cloudflare data centre that answered
	Server        string  `json:"server"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "network", Name: "speed",
			Summary: "Measure ping, jitter, download and upload speed against Cloudflare (~5 MB down, 2 MB up)",
			Args:    "[--max-mb <n>] [--json]",
			Examples: []string{
				"aos network speed",
				"aos network speed --json        # ping_ms, jitter_ms, download_mbps, upload_mbps",
				"aos network speed --max-mb 25   # bigger budget, steadier number on fast links",
			},
			Run: runNetworkSpeed,
		})
	})
}

func runNetworkSpeed(c *cli.Ctx, args []string) error {
	set, err := parseArgs(args, "max-mb")
	if err != nil {
		return err
	}
	if err := set.Reject("json", "max-mb"); err != nil {
		return err
	}
	if len(set.Rest) > 0 {
		return fmt.Errorf("`network speed` takes no positional arguments")
	}
	maxMB, err := set.Int("max-mb", speedDefaultMB)
	if err != nil {
		return err
	}
	if maxMB < 2 || maxMB > speedMaxMB {
		return fmt.Errorf("--max-mb must be between 2 and %d", speedMaxMB)
	}

	ctx, cancel := context.WithTimeout(context.Background(), speedTimeout)
	defer cancel()
	res, err := runSpeedTest(ctx, http.DefaultClient, speedBaseURL, int64(maxMB)*1_000_000)
	if err != nil {
		return &cli.ExitError{Code: 1, Message: fmt.Sprintf("speed test against %s failed: %v (offline?)", speedBaseURL, err)}
	}
	if set.Has("json") {
		return writeJSON(c, res)
	}
	c.Printf("ping      %.1f ms (jitter %.1f ms)\n", res.PingMs, res.JitterMs)
	c.Printf("download  %.1f Mbps\n", res.DownloadMbps)
	c.Printf("upload    %.1f Mbps\n", res.UploadMbps)
	where := "Cloudflare"
	if res.Colo != "" {
		where += " " + res.Colo
	}
	c.Printf("server    %s (%s down, %s up)\n", where, humanBytes(uint64(res.DownloadBytes)), humanBytes(uint64(res.UploadBytes)))
	return nil
}

// runSpeedTest does what speed.cloudflare.com does, on a fixed byte budget:
// a burst of empty requests for latency, then downloads of growing size and
// uploads, counting only the larger transfers so connection set-up and TCP
// slow start on the first one do not drag the number down.
func runSpeedTest(ctx context.Context, client *http.Client, base string, downBudget int64) (speedResult, error) {
	res := speedResult{Server: base}

	var pings []float64
	for i := 0; i < speedPings; i++ {
		ms, colo, err := speedPing(ctx, client, base)
		if err != nil {
			return res, err
		}
		if colo != "" {
			res.Colo = colo
		}
		if i == 0 {
			continue // the first one pays for DNS + TLS
		}
		pings = append(pings, ms)
	}
	res.PingMs, res.JitterMs = round1(median(pings)), round1(jitter(pings))

	var counted []transfer
	for _, n := range downloadPlan(downBudget) {
		t, err := speedDownload(ctx, client, base, n)
		if err != nil {
			return res, err
		}
		res.DownloadBytes += t.bytes
		if n >= speedStepBytes {
			counted = append(counted, t)
		}
	}
	res.DownloadMbps = round1(mbps(counted))

	counted = nil
	for _, n := range uploadPlan(downBudget) {
		t, err := speedUpload(ctx, client, base, n)
		if err != nil {
			return res, err
		}
		res.UploadBytes += t.bytes
		if n > speedUpWarm {
			counted = append(counted, t)
		}
	}
	res.UploadMbps = round1(mbps(counted))
	return res, nil
}

// downloadPlan: 100 KB to warm up, 1 MB, then one transfer that fills the
// rest of the budget.
func downloadPlan(budget int64) []int64 {
	plan := []int64{speedWarmBytes, speedStepBytes}
	if rest := budget - speedWarmBytes - speedStepBytes; rest > 0 {
		plan = append(plan, rest)
	}
	return plan
}

// uploadPlan: a 100 KB warm-up, then the rest of the (smaller) upload budget.
func uploadPlan(downBudget int64) []int64 {
	budget := int64(float64(downBudget) * speedUpFraction)
	plan := []int64{speedUpWarm}
	if rest := budget - speedUpWarm; rest > 0 {
		plan = append(plan, rest)
	}
	return plan
}

// transfer is one timed body: bytes moved and how long the body took.
type transfer struct {
	bytes int64
	dur   time.Duration
}

// speedPing times an empty download from request to first byte, minus the
// time the server says it spent (the Server-Timing header), which leaves the
// network round trip.
func speedPing(ctx context.Context, client *http.Client, base string) (float64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/__down?bytes=0", nil)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", "aos")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	elapsed := time.Since(start)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("ping: HTTP %d", resp.StatusCode)
	}
	ms := float64(elapsed.Microseconds())/1000 - serverTimingMs(resp.Header.Get("Server-Timing"))
	if ms < 0 {
		ms = 0
	}
	return ms, coloFromRay(resp.Header.Get("Cf-Ray")), nil
}

func speedDownload(ctx context.Context, client *http.Client, base string, n int64) (transfer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/__down?bytes="+strconv.FormatInt(n, 10), nil)
	if err != nil {
		return transfer{}, err
	}
	req.Header.Set("User-Agent", "aos")
	resp, err := client.Do(req)
	if err != nil {
		return transfer{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return transfer{}, fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	// The clock starts at the response headers, so the round trip to ask
	// for the bytes is not counted as transfer time.
	start := time.Now()
	got, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return transfer{}, err
	}
	return transfer{bytes: got, dur: time.Since(start)}, nil
}

func speedUpload(ctx context.Context, client *http.Client, base string, n int64) (transfer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/__up", bytes.NewReader(make([]byte, n)))
	if err != nil {
		return transfer{}, err
	}
	req.Header.Set("User-Agent", "aos")
	req.Header.Set("Content-Type", "text/plain")
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return transfer{}, err
	}
	dur := time.Since(start)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return transfer{}, fmt.Errorf("upload: HTTP %d", resp.StatusCode)
	}
	// Upload time includes the server's reply, so take its processing time off.
	dur -= time.Duration(serverTimingMs(resp.Header.Get("Server-Timing")) * float64(time.Millisecond))
	return transfer{bytes: n, dur: dur}, nil
}

// mbps is total bits over total seconds across the counted transfers.
func mbps(ts []transfer) float64 {
	var b int64
	var d time.Duration
	for _, t := range ts {
		b += t.bytes
		d += t.dur
	}
	if d <= 0 {
		return 0
	}
	return float64(b) * 8 / d.Seconds() / 1e6
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

// jitter is the mean absolute difference between consecutive pings, the
// definition speed.cloudflare.com uses.
func jitter(xs []float64) float64 {
	if len(xs) < 2 {
		return 0
	}
	var sum float64
	for i := 1; i < len(xs); i++ {
		sum += math.Abs(xs[i] - xs[i-1])
	}
	return sum / float64(len(xs)-1)
}

// serverTimingMs reads the first dur= from a Server-Timing header such as
// `cfRequestDuration;dur=12.3`. Zero when there is none.
func serverTimingMs(h string) float64 {
	for _, part := range strings.Split(h, ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(part), "dur="); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(strings.Split(v, ",")[0]), 64); err == nil {
				return f
			}
		}
	}
	return 0
}

// coloFromRay takes the data-centre code off a Cf-Ray header: "8a1b2c3d-SIN".
func coloFromRay(ray string) string {
	if i := strings.LastIndex(ray, "-"); i >= 0 {
		return ray[i+1:]
	}
	return ""
}

func round1(f float64) float64 { return math.Round(f*10) / 10 }
