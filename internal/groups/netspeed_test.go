package groups

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
)

func TestSpeedMath(t *testing.T) {
	if got := median([]float64{5, 1, 3}); got != 3 {
		t.Errorf("median odd = %v", got)
	}
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("median even = %v", got)
	}
	if got := jitter([]float64{10, 12, 9, 9}); got != (2+3+0)/3.0 {
		t.Errorf("jitter = %v", got)
	}
	if got := serverTimingMs("cfRequestDuration;dur=12.5"); got != 12.5 {
		t.Errorf("serverTiming = %v", got)
	}
	if got := serverTimingMs(""); got != 0 {
		t.Errorf("serverTiming empty = %v", got)
	}
	if got := coloFromRay("8a1b2c3d4e5f-SIN"); got != "SIN" {
		t.Errorf("colo = %q", got)
	}
	// 1 MB in 1 s = 8 Mbps; two of them still 8.
	ts := []transfer{{1_000_000, 1e9}, {1_000_000, 1e9}}
	if got := mbps(ts); got != 8 {
		t.Errorf("mbps = %v", got)
	}
	if got := mbps(nil); got != 0 {
		t.Errorf("mbps empty = %v", got)
	}
}

func TestSpeedPlansStayInBudget(t *testing.T) {
	sum := func(p []int64) (s int64) {
		for _, n := range p {
			s += n
		}
		return
	}
	if got := sum(downloadPlan(5_000_000)); got != 5_000_000 {
		t.Errorf("download plan = %d bytes, want 5 MB", got)
	}
	if got := sum(uploadPlan(5_000_000)); got != 2_000_000 {
		t.Errorf("upload plan = %d bytes, want 2 MB", got)
	}
}

func TestRunSpeedTestAgainstFakeServer(t *testing.T) {
	var mu sync.Mutex
	var downAsked, upGot int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cf-Ray", "abc-TST")
		w.Header().Set("Server-Timing", "cfRequestDuration;dur=0.1")
		switch r.URL.Path {
		case "/__down":
			n, _ := strconv.ParseInt(r.URL.Query().Get("bytes"), 10, 64)
			mu.Lock()
			downAsked += n
			mu.Unlock()
			_, _ = w.Write(make([]byte, n))
		case "/__up":
			n, _ := io.Copy(io.Discard, r.Body)
			mu.Lock()
			upGot += n
			mu.Unlock()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, err := runSpeedTest(context.Background(), srv.Client(), srv.URL, 3_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if res.DownloadBytes != 3_000_000 || downAsked != 3_000_000 {
		t.Errorf("download bytes = %d (server asked %d), want 3 MB", res.DownloadBytes, downAsked)
	}
	if res.UploadBytes != 1_200_000 || upGot != 1_200_000 {
		t.Errorf("upload bytes = %d (server got %d), want 1.2 MB", res.UploadBytes, upGot)
	}
	if res.Colo != "TST" {
		t.Errorf("colo = %q", res.Colo)
	}
	if res.DownloadMbps <= 0 || res.UploadMbps <= 0 {
		t.Errorf("speeds not measured: %+v", res)
	}
}
