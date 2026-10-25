package metrics

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"bandwidth-monitor/dns"
	"bandwidth-monitor/latency"
	"bandwidth-monitor/wifi"
)

func render(t *testing.T, reg *registry) string {
	t.Helper()
	var buf bytes.Buffer
	if _, err := reg.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return buf.String()
}

func TestExpositionFormat(t *testing.T) {
	reg := newRegistry()
	reg.gauge("b_metric", "help with \\ and\nnewline", 1.5, l("k", `quote " back\slash`+"\nnl"))
	reg.gauge("b_metric", "ignored", 2, l("k", "other"))
	reg.gauge("b_metric", "ignored", 3, l("k", "other")) // duplicate label set dropped
	reg.counter("a_total", "a counter", 42)
	reg.gauge("c_bad_utf8", "x", 0, l("v", "ok\xff"))

	got := render(t, reg)
	want := `# HELP a_total a counter
# TYPE a_total counter
a_total 42
# HELP b_metric help with \\ and\nnewline
# TYPE b_metric gauge
b_metric{k="quote \" back\\slash\nnl"} 1.5
b_metric{k="other"} 2
# HELP c_bad_utf8 x
# TYPE c_bad_utf8 gauge
c_bad_utf8{v="ok` + "\uFFFD" + `"} 0
`
	if got != want {
		t.Fatalf("unexpected exposition:\n%s\nwant:\n%s", got, want)
	}
}

// Every sample line must be syntactically valid and each family must have
// exactly one HELP/TYPE header preceding its samples.
var sampleLine = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{([a-zA-Z_][a-zA-Z0-9_]*="([^"\\]|\\.)*",?)*\})? (NaN|[+-]Inf|-?[0-9.eE+-]+)$`)

func validate(t *testing.T, out string) {
	t.Helper()
	types := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if strings.HasPrefix(line, "# TYPE ") {
			name := strings.Fields(line)[2]
			if types[name] {
				t.Errorf("duplicate TYPE for %s", name)
			}
			types[name] = true
			continue
		}
		if strings.HasPrefix(line, "# HELP ") {
			continue
		}
		if !sampleLine.MatchString(line) {
			t.Errorf("invalid sample line: %q", line)
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i >= 0 {
			name = line[:i]
		}
		if !types[name] {
			t.Errorf("sample %s without TYPE header", name)
		}
	}
}

func TestCollectNilSources(t *testing.T) {
	out := render(t, collect(Sources{}, Options{}))
	validate(t, out)
	for _, want := range []string{"bandwidth_monitor_build_info{", "bandwidth_monitor_go_goroutines ", "bandwidth_monitor_start_time_seconds "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
	if strings.Contains(out, "bandwidth_monitor_dns_") || strings.Contains(out, "bandwidth_monitor_wifi_") {
		t.Errorf("unconfigured integrations must not emit series:\n%s", out)
	}
}

type fakeDNS struct{ s *dns.Summary }

func (f fakeDNS) GetSummary() *dns.Summary { return f.s }
func (f fakeDNS) Available() bool          { return f.s != nil }
func (f fakeDNS) Run()                     {}
func (f fakeDNS) Stop()                    {}

type fakeWiFi struct{ s *wifi.Summary }

func (f fakeWiFi) GetSummary() *wifi.Summary { return f.s }
func (f fakeWiFi) Available() bool           { return f.s != nil }
func (f fakeWiFi) Run()                      {}
func (f fakeWiFi) Stop()                     {}

func TestCollectDNSAndWiFiTopNCap(t *testing.T) {
	ds := &dns.Summary{ProviderName: "AdGuard Home", TotalQueries: 1000, BlockedTotal: 250, BlockedPercent: 25, AvgLatencyMs: 12}
	for _, d := range []string{"a.example", "b.example", "c.example"} {
		ds.TopQueried = append(ds.TopQueried, dns.DomainStat{Domain: d, Count: 1})
	}
	ds.Upstreams = []dns.UpstreamStat{{Address: "tls://dns.example", Responses: 10, AvgMs: 20}}

	ws := &wifi.Summary{ProviderName: "UniFi",
		APs: []wifi.APInfo{{Name: "ap1", MAC: "aa", Model: "U6", Status: "connected", NumClients: 2, RxBytes: 5, TxBytes: 6}},
		Clients: []wifi.ClientInfo{
			{MAC: "c1", Hostname: "phone", Signal: -50, Radio: "na", RxRate: 10},
			{MAC: "c2", Signal: -80, Radio: "ng", RxRate: 100},
			{MAC: "c3", Signal: -60, Radio: "na", RxRate: 1},
		},
	}

	out := render(t, collect(Sources{DNS: fakeDNS{ds}, WiFi: fakeWiFi{ws}}, Options{TopN: 2}))
	validate(t, out)

	for _, want := range []string{
		`bandwidth_monitor_dns_up{provider="AdGuard Home"} 1`,
		`bandwidth_monitor_dns_blocked_ratio{provider="AdGuard Home"} 0.25`,
		`bandwidth_monitor_dns_latency_seconds{provider="AdGuard Home"} 0.012`,
		`bandwidth_monitor_dns_upstream_latency_seconds{provider="AdGuard Home",upstream="tls://dns.example"} 0.02`,
		`bandwidth_monitor_wifi_ap_up{provider="UniFi",ap="ap1",mac="aa",model="U6"} 1`,
		`bandwidth_monitor_wifi_ap_bytes_total{provider="UniFi",ap="ap1",mac="aa",model="U6",direction="tx"} 6`,
		`bandwidth_monitor_wifi_clients_by_signal{provider="UniFi",radio="na",quality="excellent"} 1`,
		`bandwidth_monitor_wifi_clients_by_signal{provider="UniFi",radio="ng",quality="poor"} 1`,
		`bandwidth_monitor_wifi_top_client_signal_dbm{provider="UniFi",mac="c2",hostname="c2",ssid="",ap="",radio="ng"} -80`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(out, "bandwidth_monitor_dns_top_domain_queries{"); n != 2 {
		t.Errorf("top domains not capped: got %d series, want 2", n)
	}
	if n := strings.Count(out, "bandwidth_monitor_wifi_top_client_signal_dbm{"); n != 2 {
		t.Errorf("wifi clients not capped: got %d series, want 2", n)
	}
	if strings.Contains(out, `mac="c3"`) {
		t.Errorf("lowest-throughput client should be outside top-N")
	}
}

func TestCollectUnavailableProvider(t *testing.T) {
	out := render(t, collect(Sources{DNS: fakeDNS{}}, Options{}))
	validate(t, out)
	if !strings.Contains(out, `bandwidth_monitor_dns_up{provider=""} 0`+"\n") {
		t.Errorf("expected dns_up 0, got:\n%s", out)
	}
}

func TestHandler(t *testing.T) {
	h := Handler(Sources{}, Options{TopN: 1000})

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Errorf("content type %q", ct)
	}
	validate(t, rec.Body.String())
	if !strings.Contains(rec.Body.String(), "bandwidth_monitor_scrape_duration_seconds ") {
		t.Errorf("missing scrape duration")
	}

	rec = httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/metrics", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status %d, want 405", rec.Code)
	}
}

func TestLatencyWANLabel(t *testing.T) {
	reg := newRegistry()
	st := &latency.ProbeStats{RTT: 10, AvgRTT: 12, LossPct: 5}
	collectLatencyStatus(reg, []latency.TargetStatus{
		{Name: "t.example", Alive: true, ICMPStats: st},
		{Name: "t.example", WAN: "wan0", Alive: true, ICMPStats: st},
		{Name: "t.example", WAN: "wan1", ICMPStats: &latency.ProbeStats{RTT: -1, LossPct: 100}},
	})
	out := render(t, reg)
	validate(t, out)
	for _, want := range []string{
		`bandwidth_monitor_latency_target_up{target="t.example"} 1`,
		`bandwidth_monitor_latency_target_up{target="t.example",wan="wan0"} 1`,
		`bandwidth_monitor_latency_target_up{target="t.example",wan="wan1"} 0`,
		`bandwidth_monitor_latency_rtt_seconds{target="t.example",probe="icmp"} 0.01`,
		`bandwidth_monitor_latency_rtt_seconds{target="t.example",wan="wan0",probe="icmp"} 0.01`,
		`bandwidth_monitor_latency_loss_ratio{target="t.example",probe="icmp"} 0.05`,
		`bandwidth_monitor_latency_loss_ratio{target="t.example",wan="wan1",probe="icmp"} 1`,
	} {
		if !strings.Contains(out, want+"\n") {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `latency_rtt_seconds{target="t.example",wan="wan1"`) {
		t.Error("lost probe exported an RTT")
	}
}
