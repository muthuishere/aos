package groups

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/muthuishere/aos/internal/cli"
)

// captiveProbeURL answers a bare 204 on an open internet connection. A
// captive portal intercepts the plain-http request and answers with a
// redirect or its own login page instead. Pure Go, the same on every OS.
const captiveProbeURL = "http://connectivitycheck.gstatic.com/generate_204"

// captiveInfo is what `network captive-portal --json` emits.
type captiveInfo struct {
	State  string `json:"state"`            // online, portal or offline
	Portal string `json:"portal,omitempty"` // where the portal sent us, when it said
	Status int    `json:"status,omitempty"` // the probe's HTTP status
	Probe  string `json:"probe"`
	Error  string `json:"error,omitempty"`
}

func init() {
	register(func(r *cli.Registry) {
		r.Add(&cli.Command{
			Group: "network", Name: "captive-portal",
			Summary: "Check whether a captive portal (hotel, airport Wi-Fi login) is intercepting the connection",
			Args:    "[--json]",
			Examples: []string{
				"aos network captive-portal          # online | portal <url> | offline; exit 1 unless online",
				"aos network captive-portal --json   # state, portal, status, probe",
			},
			Run: runCaptivePortal,
		})
	})
}

func runCaptivePortal(c *cli.Ctx, args []string) error {
	asJSON, err := jsonOnly("network captive-portal", args)
	if err != nil {
		return err
	}
	info := probeCaptive(captiveProbeURL)
	if asJSON {
		if err := writeJSON(c, info); err != nil {
			return err
		}
	} else if info.Portal != "" {
		c.Printf("%s %s\n", info.State, info.Portal)
	} else {
		c.Println(info.State)
	}
	if info.State != "online" {
		return &cli.ExitError{Code: 1}
	}
	return nil
}

// probeCaptive asks probeURL once, without following redirects, and reads the
// answer: 204 is online, anything else that answered is a portal.
func probeCaptive(probeURL string) captiveInfo {
	info := captiveInfo{Probe: probeURL}
	client := &http.Client{
		Timeout: 8 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		info.State, info.Error = "offline", err.Error()
		return info
	}
	req.Header.Set("User-Agent", "aos")
	resp, err := client.Do(req)
	if err != nil {
		info.State, info.Error = "offline", err.Error()
		return info
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	info.Status = resp.StatusCode
	if resp.StatusCode == http.StatusNoContent {
		info.State = "online"
		return info
	}
	info.State = "portal"
	if loc := resp.Header.Get("Location"); loc != "" {
		if u, err := resp.Request.URL.Parse(loc); err == nil {
			info.Portal = u.String()
		}
	} else if u := metaRefreshURL(string(body)); u != "" {
		info.Portal = u
	}
	return info
}

// metaRefreshURL pulls the target out of a portal page that redirects with
// <meta http-equiv="refresh" content="0; url=...">, which many portals use
// instead of a Location header.
func metaRefreshURL(page string) string {
	lower := strings.ToLower(page)
	i := strings.Index(lower, "http-equiv=\"refresh\"")
	if i < 0 {
		return ""
	}
	j := strings.Index(lower[i:], "url=")
	if j < 0 {
		return ""
	}
	rest := page[i+j+len("url="):]
	end := strings.IndexAny(rest, "\"'>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
