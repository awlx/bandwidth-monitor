package bandwidthtop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const exampleIP = "192.0.2.42"

func TestMergePreservesLocalAndFillsMissingFields(t *testing.T) {
	got := Enrichment{Country: "Exampleland", Source: "local"}
	merge(&got, Enrichment{Country: "Wrong", ASN: 64500, Provider: "Example Networks"}, "monitor")
	if got.Country != "Exampleland" || got.ASN != 64500 || got.Provider != "Example Networks" || got.Source != "local+monitor" {
		t.Fatalf("unexpected merge: %+v", got)
	}
}

func TestMonitorThenPublicFallbackMergesPartialResult(t *testing.T) {
	monitor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"ip":%q,"country_name":"Exampleland"}`, exampleIP)
	}))
	defer monitor.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"ip":%q,"country":"Wrong","provider":"Example Networks","asn":"64500"}`, exampleIP)
	}))
	defer public.Close()
	e, err := NewEnricher(Config{
		ServerURL: monitor.URL, PublicURL: public.URL, AllowHTTP: true, Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.cache[exampleIP] = cacheEntry{}
	e.mu.Unlock()
	e.resolve(exampleIP)
	got := e.Lookup(exampleIP)
	if got.Country != "Exampleland" || got.Provider != "Example Networks" || got.ASN != 64500 || got.Source != "monitor+public" {
		t.Fatalf("unexpected fallback merge: %+v", got)
	}
}

func TestMonitorValidationAndURLConstruction(t *testing.T) {
	var query, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, path = r.URL.Query().Get("ip"), r.URL.Path
		fmt.Fprintf(w, `{"ip":%q,"asn":64500,"as_org":"Example Networks"}`, exampleIP)
	}))
	defer server.Close()
	e := testEnricher(t, server.URL, true, 1)
	got, err := e.fetch(exampleIP, server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if query != exampleIP || path != "/api/host" || got.ASN != 64500 {
		t.Fatalf("unexpected result: %+v %q %q", got, path, query)
	}
}

func TestPublicJSONParsingAndIPValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ip":%q,"country":"Exampleland","city":"Testville","provider":"Example Networks","asn":"64501"}`, exampleIP)
	}))
	defer server.Close()
	e := testEnricher(t, server.URL, false, 1)
	got, err := e.fetch(exampleIP, server.URL, false)
	if err != nil || got.ASN != 64501 || got.Provider != "Example Networks" {
		t.Fatalf("got %+v, %v", got, err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ip":"198.51.100.9","asn":"64501"}`)
	}))
	defer bad.Close()
	if _, err := e.fetch(exampleIP, bad.URL, false); err == nil {
		t.Fatal("expected echoed IP mismatch")
	}
}

func TestFetchRejectsHTTPFailureAndOversizedBody(t *testing.T) {
	status := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer status.Close()
	e := testEnricher(t, status.URL, false, 1)
	if _, err := e.fetch(exampleIP, status.URL, false); err == nil {
		t.Fatal("accepted failure status")
	}

	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, strings.Repeat(" ", maxResponseBody+1))
	}))
	defer large.Close()
	if _, err := e.fetch(exampleIP, large.URL, false); err == nil {
		t.Fatal("accepted oversized response")
	}
}

func TestASNParsingIsStrict(t *testing.T) {
	for _, raw := range []string{`"AS64500"`, `-1`, `"1.5"`, `"not-a-number"`} {
		if _, err := parseASN([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestPrivateAndReservedIPsNeverSchedulePublicLookup(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer server.Close()
	e := testEnricher(t, server.URL, false, 2)
	for _, ip := range []string{"10.0.0.1", "127.0.0.1", "169.254.1.1", "192.0.2.1", "198.51.100.1", "203.0.113.1", "::", "2001:db8::1", "ff02::1"} {
		e.Lookup(ip)
	}
	e.Wait()
	if calls.Load() != 0 {
		t.Fatalf("public service called %d times", calls.Load())
	}
}

func TestPositiveAndNegativeResultsAreCached(t *testing.T) {
	for _, body := range []string{
		fmt.Sprintf(`{"ip":%q,"asn":"64500","provider":"Example Networks"}`, exampleIP),
		fmt.Sprintf(`{"ip":%q}`, exampleIP),
	} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			fmt.Fprint(w, body)
		}))
		e := testEnricher(t, server.URL, false, 2)
		e.mu.Lock()
		e.cache[exampleIP] = cacheEntry{}
		e.mu.Unlock()
		e.resolve(exampleIP)
		e.Lookup(exampleIP)
		e.Lookup(exampleIP)
		if calls.Load() != 1 {
			t.Fatalf("calls=%d for %s", calls.Load(), body)
		}
		server.Close()
	}
}

func TestConcurrencyBound(t *testing.T) {
	const limit = 2
	var active, maximum atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		for {
			old := maximum.Load()
			if n <= old || maximum.CompareAndSwap(old, n) {
				break
			}
		}
		<-release
		active.Add(-1)
		fmt.Fprintf(w, `{"ip":%q}`, r.URL.Query().Get("ip"))
	}))
	defer server.Close()
	e := testEnricher(t, server.URL, false, limit)
	var wg sync.WaitGroup
	for i := 1; i <= 6; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i)
		e.mu.Lock()
		e.cache[ip] = cacheEntry{}
		e.mu.Unlock()
		wg.Add(1)
		go func() { defer wg.Done(); e.resolve(ip) }()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	if maximum.Load() > limit {
		t.Fatalf("maximum concurrency %d", maximum.Load())
	}
}

func testEnricher(t *testing.T, publicURL string, monitor bool, concurrency int) *Enricher {
	t.Helper()
	cfg := Config{PublicURL: publicURL, DisablePublic: monitor, Concurrency: concurrency, AllowHTTP: true}
	if monitor {
		cfg.ServerURL = publicURL
	}
	e, err := NewEnricher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestHTTPAllowedOnlyForLoopbackTests(t *testing.T) {
	if _, err := NewEnricher(Config{PublicURL: "http://192.0.2.1/json", AllowHTTP: true}); err == nil {
		t.Fatal("accepted non-loopback HTTP")
	}
}
