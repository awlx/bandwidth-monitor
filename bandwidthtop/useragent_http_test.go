package bandwidthtop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestPublicRequestsAndRedirectsSendExactUserAgent(t *testing.T) {
	want := UserAgent()
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		if got := r.Header.Get("User-Agent"); got != want {
			t.Errorf("redirected User-Agent=%q, want %q", got, want)
		}
		fmt.Fprintf(w, `{"ip":%q}`, exampleIP)
	}))
	defer target.Close()
	var initialCalls atomic.Int32
	initial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initialCalls.Add(1)
		if got := r.Header.Get("User-Agent"); got != want {
			t.Errorf("initial User-Agent=%q, want %q", got, want)
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer initial.Close()
	e := testEnricher(t, Config{PublicURL: initial.URL, AllowHTTP: true})
	if _, err := e.fetch(exampleIP, initial.URL, false); err != nil {
		t.Fatal(err)
	}
	if initialCalls.Load() != 1 || targetCalls.Load() != 1 {
		t.Fatalf("initial calls=%d target calls=%d", initialCalls.Load(), targetCalls.Load())
	}
}

func TestExplicitMonitorProbeAndPeerRequestsSendExactUserAgent(t *testing.T) {
	want := UserAgent()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("User-Agent"); got != want {
			t.Errorf("User-Agent=%q, want %q", got, want)
		}
		fmt.Fprintf(w, `{"ip":%q}`, r.URL.Query().Get("ip"))
	}))
	defer server.Close()
	e, err := NewEnricher(Config{ServerURL: server.URL, DisablePublic: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Lookup("198.51.100.20")
	e.Wait()
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want readiness plus peer", calls.Load())
	}
}

func TestExplicitMonitorRedirectsRetainExactUserAgent(t *testing.T) {
	want := UserAgent()
	var initialCalls, targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalls.Add(1)
		if got := r.Header.Get("User-Agent"); got != want {
			t.Errorf("redirected User-Agent=%q, want %q", got, want)
		}
		fmt.Fprintf(w, `{"ip":%q}`, r.URL.Query().Get("ip"))
	}))
	defer target.Close()
	initial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initialCalls.Add(1)
		if got := r.Header.Get("User-Agent"); got != want {
			t.Errorf("initial User-Agent=%q, want %q", got, want)
		}
		targetURL := target.URL + r.URL.RequestURI()
		http.Redirect(w, r, targetURL, http.StatusFound)
	}))
	defer initial.Close()
	e, err := NewEnricher(Config{ServerURL: initial.URL, DisablePublic: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Lookup("198.51.100.20")
	e.Wait()
	if initialCalls.Load() != 2 || targetCalls.Load() != 2 {
		t.Fatalf("initial calls=%d target calls=%d", initialCalls.Load(), targetCalls.Load())
	}
}

func TestGatewayDiscoveryAndPeerRequestsSendExactUserAgent(t *testing.T) {
	want := UserAgent()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.Header.Get("User-Agent"); got != want {
			t.Errorf("User-Agent=%q, want %q", got, want)
		}
		fmt.Fprintf(w, `{"ip":%q}`, r.URL.Query().Get("ip"))
	}))
	defer server.Close()
	e, err := NewEnricher(Config{
		ServerURL: server.URL, MonitorDiscovery: true, DisablePublic: true,
		allowDiscoveryTestURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Lookup("203.0.113.20")
	e.Wait()
	if calls.Load() != 2 {
		t.Fatalf("calls=%d, want discovery plus peer", calls.Load())
	}
}
