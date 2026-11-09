package groups

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeCaptive(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		state   string
		portal  string
	}{
		{"open internet", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }, "online", ""},
		{"redirect", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://login.hotel.example/?next=1", http.StatusFound)
		}, "portal", "http://login.hotel.example/?next=1"},
		{"meta refresh", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`<html><meta http-equiv="refresh" content="0; url=http://10.0.0.1/login"></html>`))
		}, "portal", "http://10.0.0.1/login"},
		{"login page", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("please log in")) }, "portal", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			got := probeCaptive(srv.URL)
			if got.State != tc.state || got.Portal != tc.portal {
				t.Fatalf("got %+v, want %s %q", got, tc.state, tc.portal)
			}
		})
	}
	if got := probeCaptive("http://127.0.0.1:1/"); got.State != "offline" {
		t.Fatalf("closed port: got %+v", got)
	}
}
