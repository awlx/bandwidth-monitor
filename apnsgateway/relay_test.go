package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"bandwidth-monitor/contentstate"
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

// testRelay returns a Relay whose dial-time IP check is disabled so it can talk to the
// loopback-hosted httptest server. Everything else matches production wiring.
func testRelay() *Relay {
	r := NewRelay(nil, 10*time.Second, 0)
	r.httpClient = newFetchClient(fetchTimeout, nil)
	return r
}

// mockServer simulates a bandwidth-monitor instance. filtered controls whether it honours the
// ?iface=&since= params (new server) or ignores them and returns everything (old server).
func mockServer(t *testing.T, filtered bool, gotQueries *[]string) *httptest.Server {
	t.Helper()
	now := time.Now().UnixMilli()
	history := map[string][]contentstate.Point{
		"eth0": {{T: now - 30_000, Rx: 100, Tx: 200}, {T: now - 10_000, Rx: 300, Tx: 400}},
		"ppp0": {{T: now - 20_000, Rx: 1, Tx: 2}},
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/api/interfaces":
			json.NewEncoder(w).Encode([]remoteInterfaceStat{
				{Name: "eth0", WAN: true, RxRate: 500, TxRate: 600},
				{Name: "ppp0", RxRate: 5, TxRate: 6},
			})
		case "/api/interfaces/history":
			*gotQueries = append(*gotQueries, req.URL.RawQuery)
			out := history
			if filtered {
				if iface := req.URL.Query().Get("iface"); iface != "" {
					out = map[string][]contentstate.Point{iface: history[iface]}
				}
			}
			json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, req)
		}
	}))
}

// The relay must narrow the history fetch with ?iface=&since= so a new server can send just the
// slice contentstate.Build uses, and the built state must reflect the requested interface.
func TestFetchStateSendsFilterParams(t *testing.T) {
	var queries []string
	srv := mockServer(t, true, &queries)
	defer srv.Close()

	state, ok := testRelay().fetchState(subscription{serverURL: srv.URL, iface: "eth0"})
	if !ok {
		t.Fatal("fetchState failed")
	}
	if state.InterfaceName != "eth0" || state.RxRate != 500 {
		t.Errorf("state = %+v, want eth0 with live rx=500", state)
	}
	if len(queries) != 1 {
		t.Fatalf("want 1 history fetch, got %d (%v)", len(queries), queries)
	}
	q, _ := url.ParseQuery(queries[0])
	if q.Get("iface") != "eth0" || q.Get("since") == "" {
		t.Errorf("history query = %q, want iface=eth0 and a since param", queries[0])
	}
}

// Against an old server that ignores the params and returns every interface's history (the
// pre-params superset), the relay must still build a correct single-interface state — this is
// the no-version-negotiation fallback.
func TestFetchStateToleratesOldServerSuperset(t *testing.T) {
	var queries []string
	srv := mockServer(t, false, &queries)
	defer srv.Close()

	state, ok := testRelay().fetchState(subscription{serverURL: srv.URL, iface: "ppp0"})
	if !ok {
		t.Fatal("fetchState failed")
	}
	if state.InterfaceName != "ppp0" || state.RxRate != 5 {
		t.Errorf("state = %+v, want ppp0 with live rx=5", state)
	}
	// Points: 1 windowed history point + the synthetic now point; eth0's points must not leak in.
	if len(state.Points) != 2 {
		t.Errorf("want 2 points (ppp0 history + now), got %d: %+v", len(state.Points), state.Points)
	}
}

// A subscription naming an interface the server doesn't have must fall back to the server's WAN
// interface (refetching the filtered history for it) rather than pushing an empty state.
func TestFetchStateFallsBackForUnknownInterface(t *testing.T) {
	var queries []string
	srv := mockServer(t, true, &queries)
	defer srv.Close()

	state, ok := testRelay().fetchState(subscription{serverURL: srv.URL, iface: "wg9"})
	if !ok {
		t.Fatal("fetchState failed")
	}
	if state.InterfaceName != "eth0" {
		t.Errorf("state interface = %q, want fallback to WAN eth0", state.InterfaceName)
	}
	if len(queries) != 2 {
		t.Errorf("want 2 history fetches (wg9 miss, then eth0), got %d (%v)", len(queries), queries)
	}
}
