package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A registered serverURL must resolve only to public addresses — a bandwidth-monitor server can
// never legitimately live at a private/loopback/link-local address anyway, since the relay reaches
// it over the public internet. This is the relay's core SSRF defense: reject targets that could
// only be internal-network or cloud-metadata probing, not a real user's router.
func TestValidateServerURLRejectsNonPublicTargets(t *testing.T) {
	reject := []string{
		"http://127.0.0.1:8080",   // loopback
		"http://localhost:8080",   // loopback
		"http://192.168.1.1:8080", // RFC1918 private
		"http://10.0.0.5:8080",    // RFC1918 private
		"http://169.254.169.254/", // link-local / cloud metadata
		"http://[::1]:8080",       // IPv6 loopback
		"ftp://example.com",       // wrong scheme
		"not a url at all",        // unparseable
		"",                        // empty
	}
	for _, raw := range reject {
		if _, err := validateServerURL(raw); err == nil {
			t.Errorf("validateServerURL(%q) = nil error, want rejection", raw)
		}
	}
}

func TestValidateServerURLAcceptsPublicHTTPTarget(t *testing.T) {
	// A well-known public DNS resolver's IP, reachable and unambiguously not a private/internal
	// address — stands in for "someone's real, internet-exposed bandwidth-monitor server".
	if _, err := validateServerURL("http://8.8.8.8:8080"); err != nil {
		t.Errorf("validateServerURL(public IP) = %v, want accepted", err)
	}
}

// The SSRF check must hold at dial time, on the concrete IP the connection actually uses — a
// registration-time (or pre-fetch) LookupIP check alone can be split from the dial by DNS
// rebinding. A fetch against a loopback server with the production client must fail in the
// dialer even though no hostname pre-check ever ran on it.
func TestFetchBlocksNonPublicDialTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the server: dial-time IP check did not block loopback")
	}))
	defer srv.Close()

	r := NewRelay(nil, 10*time.Second, 0)
	var out any
	err := r.fetchJSON(srv.URL, &out)
	if err == nil || !strings.Contains(err.Error(), "non-public address") {
		t.Errorf("fetchJSON(loopback) error = %v, want dial-time non-public rejection", err)
	}
}

// With the IP check disabled (the test seam), the same client must work — this is what lets
// other tests exercise real fetches against httptest servers.
func TestFetchClientNilCheckAllowsLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	r := NewRelay(nil, 10*time.Second, 0)
	r.httpClient = newFetchClient(fetchTimeout, nil)
	var out map[string]bool
	if err := r.fetchJSON(srv.URL, &out); err != nil || !out["ok"] {
		t.Errorf("fetchJSON with nil ipCheck = (%v, %v), want ok", out, err)
	}
}

// Sources whose events have all aged out of the window must be swept from the map — otherwise a
// long-lived public relay leaks an entry per client IP ever seen.
func TestRateLimiterSweepsStaleSources(t *testing.T) {
	l := newIPRateLimiter(5, 50*time.Millisecond)
	for i := 0; i < 100; i++ {
		l.allow("10.0.0." + strconv.Itoa(i))
	}
	if got := len(l.events); got != 100 {
		t.Fatalf("expected 100 tracked sources, got %d", got)
	}

	time.Sleep(60 * time.Millisecond) // let every event age out of the window
	l.lastSweep = time.Time{}         // force the next allow() to sweep
	l.allow("192.0.2.1")

	if got := len(l.events); got != 1 {
		t.Errorf("after sweep: %d sources tracked, want only the fresh one", got)
	}
	if _, ok := l.events["192.0.2.1"]; !ok {
		t.Error("fresh source missing after sweep")
	}
}
