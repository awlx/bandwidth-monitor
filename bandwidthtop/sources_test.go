package bandwidthtop

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"bandwidth-monitor/geoip"
)

func TestMonitorReadinessProbeRunsExactlyOnceAndEnablesPeerRequests(t *testing.T) {
	var calls atomic.Int32
	var mu sync.Mutex
	var queried []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		ip := r.URL.Query().Get("ip")
		mu.Lock()
		queried = append(queried, ip)
		mu.Unlock()
		fmt.Fprintf(w, `{"ip":%q}`, ip)
	}))
	defer server.Close()

	e, err := NewEnricher(Config{ServerURL: server.URL, DisablePublic: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, ip := range []string{"192.0.2.20", "198.51.100.30", "203.0.113.40"} {
		e.Lookup(ip)
	}
	e.Wait()
	if calls.Load() != 4 {
		t.Fatalf("got %d requests, want one probe plus three peers", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if queried[0] != monitorProbeIP {
		t.Fatalf("probe used %q, want fixed %q", queried[0], monitorProbeIP)
	}
	for _, ip := range queried[1:] {
		if ip == monitorProbeIP {
			t.Fatalf("repeated readiness probe in %v", queried)
		}
	}
}

func TestFailedMonitorProbeDisablesPeerRequestsAndWarnsOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if got := r.URL.Query().Get("ip"); got != monitorProbeIP {
			t.Errorf("monitor received peer IP %q after failed probe", got)
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	e, err := NewEnricher(Config{ServerURL: server.URL, DisablePublic: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, ip := range []string{"192.0.2.20", "198.51.100.30", "203.0.113.40"} {
		e.Lookup(ip)
	}
	e.Wait()
	if calls.Load() != 1 {
		t.Fatalf("monitor called %d times after failed probe", calls.Load())
	}
	warnings := e.StartupWarnings()
	if len(warnings) != 1 || warnings[0] != "monitor enrichment disabled: startup readiness probe failed" {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if got := e.SourceSummary(); !stringsContainsAll(got, "monitor: unavailable", "public: disabled by flag") {
		t.Fatalf("unexpected source status %q", got)
	}
}

func TestPublicFallbackContinuesAfterMonitorProbeFailure(t *testing.T) {
	monitor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer monitor.Close()
	var publicCalls atomic.Int32
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publicCalls.Add(1)
		fmt.Fprintf(w, `{"ip":%q,"provider":"Example Networks","asn":"64500"}`,
			r.URL.Query().Get("ip"))
	}))
	defer public.Close()

	e, err := NewEnricher(Config{
		ServerURL: monitor.URL, PublicURL: public.URL, AllowHTTP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	e.Lookup("1.1.1.1")
	e.Wait()
	if publicCalls.Load() != 1 || e.Lookup("1.1.1.1").Provider != "Example Networks" {
		t.Fatalf("public fallback did not continue: calls=%d result=%+v",
			publicCalls.Load(), e.Lookup("1.1.1.1"))
	}
}

func TestLocalDatabasesOpenOnceReuseAndCloseOnce(t *testing.T) {
	var opens atomic.Int32
	city := &fakeLocalDatabase{result: &geoip.Result{CountryName: "Exampleland", City: "Testville"}}
	asn := &fakeLocalDatabase{result: &geoip.Result{ASN: 64500, ASOrg: "Example Networks"}}
	opener := func(countryPath, asnPath string) (localDatabase, error) {
		opens.Add(1)
		if countryPath != "" {
			return city, nil
		}
		return asn, nil
	}
	e, err := newEnricherWithDatabases(Config{DisablePublic: true}, "city.mmdb", "asn.mmdb", opener)
	if err != nil {
		t.Fatal(err)
	}
	for _, ip := range []string{"192.0.2.20", "198.51.100.30", "203.0.113.40"} {
		got := e.Lookup(ip)
		if got.Country != "Exampleland" || got.ASN != 64500 {
			t.Fatalf("partial local result: %+v", got)
		}
	}
	e.Close()
	e.Close()
	if opens.Load() != 2 || city.lookups.Load() != 3 || asn.lookups.Load() != 3 {
		t.Fatalf("opens=%d city lookups=%d ASN lookups=%d",
			opens.Load(), city.lookups.Load(), asn.lookups.Load())
	}
	if city.closes.Load() != 1 || asn.closes.Load() != 1 {
		t.Fatalf("city closes=%d ASN closes=%d", city.closes.Load(), asn.closes.Load())
	}
}

func TestInvalidOptionalDatabaseLeavesOtherLocalSourceReady(t *testing.T) {
	asn := &fakeLocalDatabase{result: &geoip.Result{ASN: 64500, ASOrg: "Example Networks"}}
	e, err := newEnricherWithDatabases(Config{DisablePublic: true}, "invalid.mmdb", "asn.mmdb",
		func(countryPath, asnPath string) (localDatabase, error) {
			if countryPath != "" {
				return nil, errors.New("invalid database")
			}
			return asn, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if got := e.Lookup("192.0.2.20"); got.ASN != 64500 {
		t.Fatalf("remaining ASN database was not used: %+v", got)
	}
	if warnings := e.StartupWarnings(); len(warnings) != 1 ||
		warnings[0] != "city/country MMDB disabled: database could not be opened" {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if got := e.SourceSummary(); !stringsContainsAll(got,
		"enrichment: local MMDB", "local MMDB: partially ready", "city unavailable", "ASN ready") {
		t.Fatalf("unexpected source status %q", got)
	}
}

func TestIndependentStartupChecksAreConcurrentAndCloseIsBounded(t *testing.T) {
	const delay = 100 * time.Millisecond
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(delay)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	start := time.Now()
	e, err := newEnricherWithDatabases(Config{
		ServerURL: server.URL, DisablePublic: true,
		HTTPClient: &http.Client{Timeout: delay},
	}, "city.mmdb", "asn.mmdb", func(string, string) (localDatabase, error) {
		time.Sleep(delay)
		return &fakeLocalDatabase{result: &geoip.Result{CountryName: "Exampleland"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 180*time.Millisecond {
		t.Fatalf("startup checks ran cumulatively: %s", elapsed)
	}
	closed := make(chan struct{})
	go func() {
		e.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close blocked after startup checks")
	}
}

func TestGatewayDiscoveryProbeEnablesPeerRequestsExactlyOnce(t *testing.T) {
	var calls atomic.Int32
	var queries []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		ip := r.URL.Query().Get("ip")
		mu.Lock()
		queries = append(queries, ip)
		mu.Unlock()
		fmt.Fprintf(w, `{"ip":%q}`, ip)
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
	e.Lookup("192.0.2.20")
	e.Lookup("198.51.100.30")
	e.Wait()
	if calls.Load() != 3 {
		t.Fatalf("got %d requests, want one discovery plus two peers", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if queries[0] != monitorProbeIP || queries[1] == monitorProbeIP || queries[2] == monitorProbeIP {
		t.Fatalf("unexpected query sequence %v", queries)
	}
	if !strings.Contains(e.SourceSummary(), "monitor: discovered") {
		t.Fatalf("unexpected source status %q", e.SourceSummary())
	}
}

func TestGatewayDiscoveryFailureInvalidResponseAndRedirectDisableOnce(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
	}{
		{
			name: "failure status",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}),
		},
		{
			name: "invalid response",
			handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `{"ip":"198.51.100.99"}`)
			}),
		},
		{
			name: "redirect",
			handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/elsewhere", http.StatusFound)
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				test.handler.ServeHTTP(w, r)
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
			e.Lookup("192.0.2.20")
			e.Wait()
			if calls.Load() != 1 {
				t.Fatalf("discovery retried or sent peer request: %d", calls.Load())
			}
			warnings := e.StartupWarnings()
			if len(warnings) != 1 ||
				warnings[0] != "gateway monitor discovery unavailable: startup probe failed" {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			if !strings.Contains(e.SourceSummary(), "monitor: unavailable") {
				t.Fatalf("unexpected source status %q", e.SourceSummary())
			}
		})
	}
}

func TestGatewayDiscoveryBypassesConfiguredProxy(t *testing.T) {
	var originCalls, proxyCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		fmt.Fprintf(w, `{"ip":%q}`, r.URL.Query().Get("ip"))
	}))
	defer origin.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		proxyCalls.Add(1)
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	e, err := NewEnricher(Config{
		ServerURL: origin.URL, MonitorDiscovery: true, DisablePublic: true,
		HTTPClient:            &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}},
		allowDiscoveryTestURL: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	if originCalls.Load() != 1 || proxyCalls.Load() != 0 {
		t.Fatalf("origin calls=%d proxy calls=%d", originCalls.Load(), proxyCalls.Load())
	}
}

func TestDisabledGatewayDiscoveryHasNoProbe(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	endpoint, discovered := MonitorServerURL("", true, net.ParseIP("192.0.2.1"), "eth0")
	if endpoint != "" || discovered {
		t.Fatalf("opt-out selected endpoint %q", endpoint)
	}
	e, err := NewEnricher(Config{DisableMonitorDiscovery: true, DisablePublic: true})
	if err != nil {
		t.Fatal(err)
	}
	e.Close()
	if calls.Load() != 0 || !strings.Contains(e.SourceSummary(), "monitor: disabled by flag") {
		t.Fatalf("calls=%d status=%q", calls.Load(), e.SourceSummary())
	}
}

func TestSourceSummaryDescribesEveryFallbackCombination(t *testing.T) {
	tests := []struct {
		name                   string
		local, monitor, public bool
		want                   string
	}{
		{"none", false, false, false, "enrichment: none"},
		{"local", true, false, false, "enrichment: local MMDB"},
		{"monitor", false, true, false, "enrichment: monitor"},
		{"public", false, false, true, "enrichment: public fallback"},
		{"local monitor", true, true, false, "enrichment: local MMDB -> monitor"},
		{"local public", true, false, true, "enrichment: local MMDB -> public"},
		{"monitor public", false, true, true, "enrichment: monitor -> public"},
		{"all", true, true, true, "enrichment: local MMDB -> monitor -> public"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := &Enricher{
				cfg:          Config{DisablePublic: !test.public},
				monitorState: "not discovered",
			}
			if test.local {
				e.localDBs = &LocalDatabases{cityState: "ready", asnState: "not configured"}
			}
			if test.monitor {
				e.cfg.ServerURL = "https://example.com"
				e.monitorState = "configured"
			}
			if got := strings.SplitN(e.SourceSummary(), ";", 2)[0]; got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestSourceSummaryUsesReasonedSourceStates(t *testing.T) {
	tests := []struct {
		name          string
		local         *LocalDatabases
		monitorState  string
		disablePublic bool
		want          []string
	}{
		{
			name:         "not configured and not discovered",
			monitorState: "not discovered",
			want:         []string{"local MMDB: not configured", "monitor: not discovered", "public: enabled"},
		},
		{
			name:         "local unavailable and discovery unavailable",
			local:        &LocalDatabases{cityState: "unavailable", asnState: "not configured"},
			monitorState: "unavailable", disablePublic: true,
			want: []string{"local MMDB: unavailable", "monitor: unavailable", "public: disabled by flag"},
		},
		{
			name:         "local ready and monitor discovered",
			local:        &LocalDatabases{cityState: "ready", asnState: "ready"},
			monitorState: "discovered",
			want:         []string{"local MMDB: ready", "monitor: discovered", "public: enabled"},
		},
		{
			name:         "monitor disabled explicitly",
			monitorState: "disabled by flag",
			want:         []string{"monitor: disabled by flag"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := &Enricher{
				cfg:      Config{DisablePublic: test.disablePublic},
				localDBs: test.local, monitorState: test.monitorState,
			}
			for _, want := range test.want {
				if !strings.Contains(e.SourceSummary(), want) {
					t.Fatalf("summary %q missing %q", e.SourceSummary(), want)
				}
			}
		})
	}
}

func TestSourceStatusLinesWrapWholeStatesAtNarrowWidths(t *testing.T) {
	e := &Enricher{
		cfg:          Config{DisablePublic: true},
		localDBs:     &LocalDatabases{cityState: "unavailable", asnState: "not configured"},
		monitorState: "disabled by flag",
	}
	lines := e.SourceStatusLines(32)
	if len(lines) != 4 {
		t.Fatalf("got %d lines, want chain plus three source states: %q", len(lines), lines)
	}
	for _, line := range lines {
		if displayWidth(line) > 32 {
			t.Fatalf("line exceeds width: %q", line)
		}
	}
	for _, want := range []string{
		"local MMDB: unavailable",
		"monitor: disabled by flag",
		"public: disabled by flag",
	} {
		if !containsString(lines, want) {
			t.Fatalf("narrow lines %q split or dropped %q", lines, want)
		}
	}
	for width := 1; width <= 100; width++ {
		for _, line := range e.SourceStatusLines(width) {
			if displayWidth(line) > width {
				t.Fatalf("width %d produced %d columns: %q", width, displayWidth(line), line)
			}
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type fakeLocalDatabase struct {
	result  *geoip.Result
	lookups atomic.Int32
	closes  atomic.Int32
}

func (d *fakeLocalDatabase) Available() bool { return true }

func (d *fakeLocalDatabase) Lookup(string) *geoip.Result {
	d.lookups.Add(1)
	copy := *d.result
	return &copy
}

func (d *fakeLocalDatabase) Close() { d.closes.Add(1) }

func stringsContainsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
