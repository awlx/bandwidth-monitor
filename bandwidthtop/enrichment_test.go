package bandwidthtop

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const exampleIP = "192.0.2.42"

func TestMergePreservesHigherPriorityFields(t *testing.T) {
	got := Enrichment{Country: "Exampleland", ASN: 64500, Source: "local"}
	merge(&got, Enrichment{
		Hostname: "peer.example", Country: "Wrong", City: "Testville",
		ASN: 64501, Provider: "Example Networks",
	}, "monitor")
	if got.Country != "Exampleland" || got.ASN != 64500 ||
		got.Hostname != "peer.example" || got.Provider != "Example Networks" ||
		got.Source != "local+monitor" {
		t.Fatalf("unexpected merge: %+v", got)
	}
}

func TestLocalASNStillFallsThroughForDisplayFields(t *testing.T) {
	monitor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"ip":%q,"hostname":"peer.example","country_name":"Exampleland","city":"Testville","as_org":"Example Networks","asn":64501}`, exampleIP)
	}))
	defer monitor.Close()
	e := testEnricher(t, Config{
		ServerURL: monitor.URL, DisablePublic: true, Concurrency: 1,
	})
	if !e.enqueue(exampleIP, Enrichment{ASN: 64500, Source: "local"}) {
		t.Fatal("failed to enqueue")
	}
	e.Wait()
	got := e.Lookup(exampleIP)
	if got.ASN != 64500 || got.Hostname != "peer.example" ||
		got.Country != "Exampleland" || got.Provider != "Example Networks" ||
		got.Source != "local+monitor" {
		t.Fatalf("unexpected partial fallback: %+v", got)
	}
}

func TestMonitorThenPublicFallbackMergesPartialResult(t *testing.T) {
	monitor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"ip":%q,"country_name":"Exampleland"}`, exampleIP)
	}))
	defer monitor.Close()
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"ip":%q,"country":"Wrong","city":"Testville","hostname":"peer.example","provider":"Example Networks","asn":"64500"}`, exampleIP)
	}))
	defer public.Close()
	e := testEnricher(t, Config{
		ServerURL: monitor.URL, PublicURL: public.URL, AllowHTTP: true, Concurrency: 1,
	})
	e.mu.Lock()
	e.cache[exampleIP] = cacheEntry{done: true}
	e.order = append(e.order, exampleIP)
	e.mu.Unlock()
	// Directly exercise the ordered source chain because documentation ranges
	// are intentionally suppressed by the normal public scheduling path.
	e.resolveWithPublicForTest(exampleIP)
	got := e.Lookup(exampleIP)
	if got.Country != "Exampleland" || got.Provider != "Example Networks" ||
		got.ASN != 64500 || got.Source != "monitor+public" {
		t.Fatalf("unexpected fallback merge: %+v", got)
	}
}

func (e *Enricher) resolveWithPublicForTest(ip string) {
	e.mu.RLock()
	out := e.cache[ip].value
	e.mu.RUnlock()
	value, err := e.fetch(ip, e.cfg.ServerURL, true)
	if err == nil {
		merge(&out, value, "monitor")
	}
	value, err = e.fetch(ip, e.cfg.PublicURL, false)
	if err == nil {
		merge(&out, value, "public")
	}
	e.mu.Lock()
	e.cache[ip] = cacheEntry{value: out, done: true}
	e.mu.Unlock()
}

func TestMonitorValidationAndURLConstruction(t *testing.T) {
	var query, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, path = r.URL.Query().Get("ip"), r.URL.Path
		fmt.Fprintf(w, `{"ip":%q,"asn":64500,"as_org":"Example Networks"}`, exampleIP)
	}))
	defer server.Close()
	e := testEnricher(t, Config{ServerURL: server.URL, DisablePublic: true})
	got, err := e.fetch(exampleIP, server.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	if query != exampleIP || path != "/api/host" || got.ASN != 64500 {
		t.Fatalf("unexpected result: %+v %q %q", got, path, query)
	}
}

func TestPublicJSONParsingAndIPValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"ip":%q,"country":"Exampleland","city":"Testville","provider":"Example Networks","asn":"64501"}`, exampleIP)
	}))
	defer server.Close()
	e := testEnricher(t, Config{PublicURL: server.URL, AllowHTTP: true})
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

func TestPublicRedirectToHTTPRejectedBeforeFollow(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "http://192.0.2.9/result", http.StatusFound)
	}))
	defer server.Close()
	e := testEnricher(t, Config{PublicURL: server.URL, AllowHTTP: true})
	if _, err := e.fetch(exampleIP, server.URL, false); err == nil {
		t.Fatal("accepted redirect to non-loopback HTTP")
	}
	if requests.Load() != 1 {
		t.Fatalf("followed unsafe redirect: %d requests", requests.Load())
	}
}

func TestPublicRedirectToLoopbackHTTPSRejectedBeforeFollow(t *testing.T) {
	var targetRequests atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	e := testEnricher(t, Config{PublicURL: server.URL, AllowHTTP: true})
	if _, err := e.fetch(exampleIP, server.URL, false); err == nil {
		t.Fatal("accepted redirect to loopback HTTPS")
	}
	if targetRequests.Load() != 0 {
		t.Fatal("unsafe redirect destination was contacted")
	}
}

func TestPublicRedirectToPrivateHTTPSRejectedBeforeFollow(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Redirect(w, r, "https://10.0.0.1/result", http.StatusFound)
	}))
	defer server.Close()
	e := testEnricher(t, Config{PublicURL: server.URL, AllowHTTP: true})
	if _, err := e.fetch(exampleIP, server.URL, false); err == nil {
		t.Fatal("accepted redirect to private HTTPS")
	}
	if requests.Load() != 1 {
		t.Fatalf("followed unsafe redirect: %d requests", requests.Load())
	}
}

func TestPublicHostnameResolvingPrivateRejected(t *testing.T) {
	u, _ := url.Parse("https://service.example/query")
	err := validatePublicDestination(context.Background(), u, false,
		func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		})
	if err == nil {
		t.Fatal("accepted hostname resolving to private address")
	}
}

func TestPublicDialRevalidatesDNS(t *testing.T) {
	var lookups atomic.Int32
	e := testEnricher(t, Config{
		PublicURL: "http://localhost:1/query",
		AllowHTTP: true,
		lookupIP: func(context.Context, string) ([]net.IP, error) {
			if lookups.Add(1) == 1 {
				return []net.IP{net.ParseIP("127.0.0.1")}, nil
			}
			return []net.IP{net.ParseIP("10.0.0.1")}, nil
		},
	})
	if _, err := e.fetch(exampleIP, e.cfg.PublicURL, false); err == nil {
		t.Fatal("accepted DNS rebinding to private address")
	}
	if lookups.Load() < 2 {
		t.Fatal("destination was not revalidated at dial time")
	}
}

func TestHTTPSDialNeverUsesLoopbackTestBypass(t *testing.T) {
	ctx := context.WithValue(context.Background(), publicSchemeContextKey{}, "https")
	dial := publicDialContext(true, func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	})
	_, err := dial(ctx, "tcp", "service.example:443")
	if err == nil || !strings.Contains(err.Error(), "disallowed address") {
		t.Fatalf("HTTPS loopback dial was not rejected: %v", err)
	}
}

func TestHTTPSURLPolicyAcceptedBeforeResolution(t *testing.T) {
	u, _ := url.Parse("https://service.example/query")
	if !publicURLAllowed(u, false) {
		t.Fatal("rejected HTTPS public URL")
	}
}

func TestFetchRejectsHTTPFailureAndOversizedBody(t *testing.T) {
	status := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer status.Close()
	e := testEnricher(t, Config{PublicURL: status.URL, AllowHTTP: true})
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
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	e := testEnricher(t, Config{PublicURL: server.URL, AllowHTTP: true})
	for _, ip := range []string{
		"10.0.0.1", "127.0.0.1", "169.254.1.1", "192.0.2.1",
		"198.51.100.1", "203.0.113.1", "::", "2001:db8::1", "ff02::1",
	} {
		e.Lookup(ip)
	}
	e.Wait()
	if calls.Load() != 0 {
		t.Fatalf("public service called %d times", calls.Load())
	}
}

func TestPositiveAndNegativeResultsAreCached(t *testing.T) {
	for _, body := range []string{
		fmt.Sprintf(`{"ip":%q,"hostname":"peer.example","country":"Exampleland","city":"Testville","asn":"64500","as_org":"Example Networks"}`, exampleIP),
		fmt.Sprintf(`{"ip":%q}`, exampleIP),
	} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			fmt.Fprint(w, body)
		}))
		e := testEnricher(t, Config{ServerURL: server.URL, DisablePublic: true})
		e.Lookup(exampleIP)
		e.Wait()
		e.Lookup(exampleIP)
		e.Lookup(exampleIP)
		if calls.Load() != 1 {
			t.Fatalf("calls=%d for %s", calls.Load(), body)
		}
		e.Close()
		server.Close()
	}
}

func TestCacheQueueAndConcurrencyAreBounded(t *testing.T) {
	const (
		concurrency = 2
		queueSize   = 3
		cacheSize   = concurrency + queueSize
	)
	release := make(chan struct{})
	var active, maximum atomic.Int32
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
	e := testEnricher(t, Config{
		ServerURL: server.URL, DisablePublic: true, Concurrency: concurrency,
		QueueSize: queueSize, CacheSize: cacheSize,
	})
	for i := 1; i <= 100; i++ {
		e.Lookup("192.0.2." + strconv.Itoa(i))
	}
	time.Sleep(50 * time.Millisecond)
	e.mu.RLock()
	gotCache, gotQueue := len(e.cache), len(e.jobs)
	e.mu.RUnlock()
	exceeded := gotCache > cacheSize || gotQueue > queueSize || maximum.Load() > concurrency
	close(release)
	e.Wait()
	if exceeded {
		t.Fatalf("bounds exceeded: cache=%d queue=%d concurrency=%d", gotCache, gotQueue, maximum.Load())
	}
}

func TestDuplicateInflightLookupIsDeduplicated(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		fmt.Fprintf(w, `{"ip":%q}`, r.URL.Query().Get("ip"))
	}))
	defer server.Close()
	e := testEnricher(t, Config{ServerURL: server.URL, DisablePublic: true})
	var callers sync.WaitGroup
	for i := 0; i < 50; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			e.Lookup(exampleIP)
		}()
	}
	callers.Wait()
	close(release)
	e.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate lookup count %d", calls.Load())
	}
}

func TestCompletedCacheEvictsFIFOAtBound(t *testing.T) {
	e := testEnricher(t, Config{DisablePublic: true, CacheSize: 3})
	for i := 1; i <= 10; i++ {
		e.Lookup("10.0.0." + strconv.Itoa(i))
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.cache) != 3 {
		t.Fatalf("cache size %d", len(e.cache))
	}
	if _, ok := e.cache["10.0.0.1"]; ok {
		t.Fatal("oldest completed entry was not evicted")
	}
}

func TestWaitReturnsAfterCloseWithPendingEntries(t *testing.T) {
	e := testEnricher(t, Config{DisablePublic: true})
	e.mu.Lock()
	e.cache[exampleIP] = cacheEntry{}
	e.order = append(e.order, exampleIP)
	e.mu.Unlock()
	e.Close()

	done := make(chan struct{})
	go func() {
		e.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait blocked after Close")
	}
}

func TestCloseDoesNotDrainQueuedLookups(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		fmt.Fprintf(w, `{"ip":%q}`, r.URL.Query().Get("ip"))
	}))
	defer server.Close()
	e := testEnricher(t, Config{
		ServerURL: server.URL, DisablePublic: true, Concurrency: 1, QueueSize: 2,
	})
	e.Lookup("192.0.2.1")
	<-started
	e.Lookup("192.0.2.2")
	closed := make(chan struct{})
	go func() {
		e.Close()
		close(closed)
	}()
	for {
		e.mu.RLock()
		stopping := e.closed
		e.mu.RUnlock()
		if stopping {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not stop queued lookups")
	}
	if calls.Load() != 1 {
		t.Fatalf("processed %d lookups after Close", calls.Load())
	}
}

func testEnricher(t *testing.T, cfg Config) *Enricher {
	t.Helper()
	e, err := NewEnricher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	return e
}

func TestHTTPAllowedOnlyForLoopbackTests(t *testing.T) {
	if _, err := NewEnricher(Config{
		PublicURL: "http://192.0.2.1/json", AllowHTTP: true,
	}); err == nil {
		t.Fatal("accepted non-loopback HTTP")
	}
}
