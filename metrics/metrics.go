// Package metrics exposes the daemon's live statistics in the Prometheus text
// exposition format on /metrics.
//
// Every scrape reads the same in-memory snapshots that back the /api/*
// endpoints, so enabling it adds no background work. Per-host series (top
// talkers, clients, DNS domains, conntrack hosts, Wi-Fi clients, ASNs) are
// capped at Options.TopN to keep label cardinality bounded.
package metrics

import (
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"bandwidth-monitor/collector"
	"bandwidth-monitor/conntrack"
	"bandwidth-monitor/dns"
	"bandwidth-monitor/latency"
	"bandwidth-monitor/resolver"
	"bandwidth-monitor/speedtest"
	"bandwidth-monitor/talkers"
	"bandwidth-monitor/topology"
	"bandwidth-monitor/version"
	"bandwidth-monitor/wifi"
)

const (
	// DefaultTopN is the default number of entries exported per top-N list.
	DefaultTopN = 10
	// MaxTopN bounds METRICS_TOP_N so a misconfiguration cannot explode
	// series cardinality.
	MaxTopN = 100

	ns = "bandwidth_monitor_"
)

// Sources are the subsystems scraped on every request. Any field may be nil
// when the corresponding feature is disabled or not configured.
type Sources struct {
	Collector *collector.Collector
	Talkers   *talkers.Tracker
	DNS       dns.Provider
	Resolver  *resolver.Resolver
	WiFi      wifi.Provider
	Conntrack *conntrack.Tracker
	Latency   *latency.Monitor
	Speedtest *speedtest.Tester
	Topology  *topology.Scanner
}

// Options tunes the exported series.
type Options struct {
	// TopN caps every per-host / per-domain / per-ASN list.
	TopN int
}

var startTime = time.Now()

// Handler returns an http.HandlerFunc serving the Prometheus exposition.
func Handler(src Sources, opts Options) http.HandlerFunc {
	if opts.TopN <= 0 {
		opts.TopN = DefaultTopN
	}
	if opts.TopN > MaxTopN {
		opts.TopN = MaxTopN
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		start := time.Now()
		reg := collect(src, opts)
		reg.gauge(ns+"scrape_duration_seconds", "Time spent building this /metrics response.", time.Since(start).Seconds())
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodHead {
			return
		}
		reg.WriteTo(w)
	}
}

// collect gathers all metrics into a fresh registry.
func collect(src Sources, opts Options) *registry {
	if opts.TopN <= 0 {
		opts.TopN = DefaultTopN
	}
	reg := newRegistry()
	collectBuild(reg)
	collectInterfaces(reg, src.Collector)
	collectTalkers(reg, src.Talkers, opts.TopN)
	collectDNS(reg, src.DNS, src.Resolver, opts.TopN)
	collectWiFi(reg, src.WiFi, opts.TopN)
	collectConntrack(reg, src.Conntrack, opts.TopN)
	collectLatency(reg, src.Latency)
	collectSpeedtest(reg, src.Speedtest)
	collectTopology(reg, src.Topology)
	return reg
}

func l(name, value string) Label { return Label{Name: name, Value: value} }

func collectBuild(reg *registry) {
	reg.gauge(ns+"build_info", "Build information; value is always 1.", 1,
		l("version", version.Version), l("commit", version.Commit), l("goversion", runtime.Version()))
	reg.gauge(ns+"start_time_seconds", "Unix time the daemon started.", float64(startTime.Unix()))

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	reg.gauge(ns+"go_goroutines", "Number of goroutines.", float64(runtime.NumGoroutine()))
	reg.gauge(ns+"go_heap_alloc_bytes", "Bytes of allocated heap objects.", float64(ms.HeapAlloc))
	reg.gauge(ns+"go_heap_inuse_bytes", "Bytes in in-use heap spans.", float64(ms.HeapInuse))
	reg.gauge(ns+"go_sys_bytes", "Total bytes of memory obtained from the OS by the Go runtime.", float64(ms.Sys))
	reg.counter(ns+"go_gc_cycles_total", "Completed GC cycles.", float64(ms.NumGC))
}

// --- interfaces --------------------------------------------------------------

func collectInterfaces(reg *registry, c *collector.Collector) {
	if c == nil {
		return
	}
	for _, s := range c.GetAll() {
		wan := strconv.FormatBool(s.WAN)
		base := []Label{l("interface", s.Name), l("type", s.IfaceType), l("wan", wan)}
		with := func(extra ...Label) []Label {
			return append(append([]Label(nil), base...), extra...)
		}
		reg.gauge(ns+"interface_info", "Interface metadata; value is always 1.", 1,
			with(l("oper_state", s.OperState))...)
		reg.gauge(ns+"interface_up", "1 if the interface operational state is up.",
			boolFloat(strings.EqualFold(s.OperState, "up")), base...)
		if s.Speed > 0 {
			reg.gauge(ns+"interface_speed_bits_per_second", "Negotiated link speed.", float64(s.Speed)*1e6, base...)
		}
		reg.gauge(ns+"interface_vpn_routing", "1 if traffic is currently routed via this VPN interface.", boolFloat(s.VPNRouting), base...)

		for _, d := range []struct {
			dir                             string
			bytes, packets, errors, dropped uint64
			rate, pps                       float64
		}{
			{"rx", s.RxBytes, s.RxPackets, s.RxErrors, s.RxDropped, s.RxRate, s.RxPPS},
			{"tx", s.TxBytes, s.TxPackets, s.TxErrors, s.TxDropped, s.TxRate, s.TxPPS},
		} {
			dl := with(l("direction", d.dir))
			reg.counter(ns+"interface_bytes_total", "Bytes received (rx) / transmitted (tx) by the interface.", float64(d.bytes), dl...)
			reg.counter(ns+"interface_packets_total", "Packets received / transmitted by the interface.", float64(d.packets), dl...)
			reg.counter(ns+"interface_errors_total", "Receive / transmit errors on the interface.", float64(d.errors), dl...)
			reg.counter(ns+"interface_dropped_total", "Receive / transmit drops on the interface.", float64(d.dropped), dl...)
			reg.gauge(ns+"interface_rate_bytes_per_second", "Current throughput as computed by the collector.", d.rate, dl...)
			reg.gauge(ns+"interface_rate_packets_per_second", "Current packet rate as computed by the collector.", d.pps, dl...)
		}
	}
}

// --- talkers -----------------------------------------------------------------

func talkerLabels(t talkers.TalkerStat) []Label {
	asn := ""
	if t.ASN != 0 {
		asn = strconv.FormatUint(uint64(t.ASN), 10)
	}
	host := t.Hostname
	if host == "" {
		host = t.IP
	}
	return []Label{l("ip", t.IP), l("hostname", host), l("country", t.Country), l("asn", asn), l("as_org", t.ASOrg)}
}

func emitTalkerList(reg *registry, list []talkers.TalkerStat, name, help string, rate bool) {
	for _, t := range list {
		base := talkerLabels(t)
		rx, tx := float64(t.RxBytes), float64(t.TxBytes)
		if rate {
			rx, tx = t.RxRate, t.TxRate
		}
		reg.gauge(name, help, rx, append(append([]Label(nil), base...), l("direction", "rx"))...)
		reg.gauge(name, help, tx, append(append([]Label(nil), base...), l("direction", "tx"))...)
	}
}

func collectTalkers(reg *registry, t *talkers.Tracker, n int) {
	if t == nil {
		return
	}
	emitTalkerList(reg, t.TopByBandwidth(n), ns+"top_talker_rate_bytes_per_second",
		"Current rate of the top-N hosts by bandwidth (rolling ~30s window).", true)
	emitTalkerList(reg, t.TopByVolume(n), ns+"top_talker_volume_bytes",
		"Traffic of the top-N hosts by volume over the rolling 24h window.", false)
	emitTalkerList(reg, t.TopClientsByBandwidth(n), ns+"top_client_rate_bytes_per_second",
		"Current rate of the top-N local clients by bandwidth (rolling ~30s window).", true)
	emitTalkerList(reg, t.TopClientsByVolume(n), ns+"top_client_volume_bytes",
		"Traffic of the top-N local clients by volume over the rolling 24h window.", false)

	devs := make([]string, 0)
	stats := t.CaptureStats()
	for d := range stats {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	for _, d := range devs {
		st := stats[d]
		reg.counter(ns+"capture_packets_total", "Packets the kernel delivered to the talkers capture ring.", float64(st.Packets), l("device", d))
		reg.counter(ns+"capture_drops_total", "Packets the kernel dropped because the talkers capture ring was full.", float64(st.Drops), l("device", d))
	}

	reg.gauge(ns+"talkers_unique_ips", "Distinct IPs seen in the rolling 24h window.", float64(t.UniqueIPs()))
	for proto, b := range t.GetProtocolBreakdown() {
		reg.gauge(ns+"protocol_volume_bytes", "Traffic per L4 protocol over the rolling 24h window.", float64(b), l("protocol", proto))
	}
	for svc, b := range t.GetServiceBreakdown() {
		reg.gauge(ns+"service_volume_bytes", "Traffic per service (classified by well-known port) over the rolling 24h window.", float64(b), l("service", svc))
	}
	for ver, b := range t.GetIPVersionBreakdown() {
		reg.gauge(ns+"ip_version_volume_bytes", "Traffic per IP version over the rolling 24h window.", float64(b), l("ip_version", ver))
	}

	geo := t.GetGeoBreakdown()
	if geo == nil {
		return
	}
	for _, c := range geo.Countries {
		ll := []Label{l("country", c.Country), l("country_name", c.CountryName)}
		reg.gauge(ns+"country_volume_bytes", "Remote traffic per country over the rolling 24h window (top 20).", float64(c.Bytes), ll...)
		reg.gauge(ns+"country_remote_ips", "Distinct remote IPs per country over the rolling 24h window (top 20).", float64(c.Connections), ll...)
	}
	for i, a := range geo.ASNs {
		if i >= n {
			break
		}
		ll := []Label{l("asn", strconv.FormatUint(uint64(a.ASN), 10)), l("as_org", a.ASOrg)}
		reg.gauge(ns+"asn_volume_bytes", "Remote traffic per ASN over the rolling 24h window (top-N).", float64(a.Bytes), ll...)
		reg.gauge(ns+"asn_remote_ips", "Distinct remote IPs per ASN over the rolling 24h window (top-N).", float64(a.Connections), ll...)
	}
}

// --- DNS ---------------------------------------------------------------------

func collectDNS(reg *registry, p dns.Provider, res *resolver.Resolver, n int) {
	if p == nil {
		return
	}
	s := p.GetSummary()
	name := ""
	if s != nil {
		name = s.ProviderName
	}
	pl := l("provider", name)
	reg.gauge(ns+"dns_up", "1 if the DNS integration is reachable.", boolFloat(p.Available() && s != nil), pl)
	if s == nil {
		return
	}
	reg.gauge(ns+"dns_queries", "DNS queries in the provider's statistics window.", float64(s.TotalQueries), pl)
	reg.gauge(ns+"dns_blocked_queries", "Blocked DNS queries in the provider's statistics window.", float64(s.BlockedTotal), pl)
	reg.gauge(ns+"dns_blocked_ratio", "Fraction of DNS queries blocked (0-1).", s.BlockedPercent/100, pl)
	reg.gauge(ns+"dns_latency_seconds", "Average DNS processing time.", s.AvgLatencyMs/1000, pl)
	for i, d := range s.TopQueried {
		if i >= n {
			break
		}
		reg.gauge(ns+"dns_top_domain_queries", "Queries for the top-N most queried domains.", float64(d.Count), pl, l("domain", d.Domain))
	}
	for i, d := range s.TopBlocked {
		if i >= n {
			break
		}
		reg.gauge(ns+"dns_top_blocked_domain_queries", "Queries for the top-N most blocked domains.", float64(d.Count), pl, l("domain", d.Domain))
	}
	for i, c := range s.TopClients {
		if i >= n {
			break
		}
		host := c.Hostname
		if res != nil && (host == "" || host == c.IP) {
			host = res.LookupAddrAsync(c.IP)
		}
		if host == "" {
			host = c.IP
		}
		reg.gauge(ns+"dns_top_client_queries", "Queries from the top-N DNS clients.", float64(c.Count), pl, l("ip", c.IP), l("hostname", host))
	}
	for _, u := range s.Upstreams {
		ul := []Label{pl, l("upstream", u.Address)}
		reg.gauge(ns+"dns_upstream_responses", "Responses per upstream resolver in the statistics window.", float64(u.Responses), ul...)
		reg.gauge(ns+"dns_upstream_latency_seconds", "Average response time per upstream resolver.", u.AvgMs/1000, ul...)
	}
}

// --- Wi-Fi -------------------------------------------------------------------

func signalQuality(dbm int) string {
	switch {
	case dbm == 0:
		return "unknown"
	case dbm >= -55:
		return "excellent"
	case dbm >= -67:
		return "good"
	case dbm >= -75:
		return "fair"
	default:
		return "poor"
	}
}

func collectWiFi(reg *registry, p wifi.Provider, n int) {
	if p == nil {
		return
	}
	s := p.GetSummary()
	name := ""
	if s != nil {
		name = s.ProviderName
	}
	pl := l("provider", name)
	reg.gauge(ns+"wifi_up", "1 if the Wi-Fi controller integration is reachable.", boolFloat(p.Available() && s != nil), pl)
	if s == nil {
		return
	}
	reg.gauge(ns+"wifi_access_points", "Number of access points.", float64(len(s.APs)), pl)
	reg.gauge(ns+"wifi_clients", "Number of wireless clients.", float64(len(s.Clients)), pl)
	reg.gauge(ns+"wifi_switches", "Number of managed switches.", float64(len(s.Switches)), pl)
	reg.gauge(ns+"wifi_wired_clients", "Number of wired clients known to the controller.", float64(len(s.WiredClients)), pl)

	for _, ap := range s.APs {
		al := []Label{pl, l("ap", ap.Name), l("mac", ap.MAC), l("model", ap.Model)}
		reg.gauge(ns+"wifi_ap_up", "1 if the access point is connected to the controller.", boolFloat(ap.Status == "connected"), al...)
		reg.gauge(ns+"wifi_ap_clients", "Wireless clients per access point.", float64(ap.NumClients), al...)
		reg.gauge(ns+"wifi_ap_uptime_seconds", "Access point uptime.", float64(ap.Uptime), al...)
		reg.counter(ns+"wifi_ap_bytes_total", "Bytes transferred by the access point (resets on AP reboot).", float64(ap.RxBytes), append(al, l("direction", "rx"))...)
		reg.counter(ns+"wifi_ap_bytes_total", "Bytes transferred by the access point (resets on AP reboot).", float64(ap.TxBytes), append(al, l("direction", "tx"))...)
		reg.gauge(ns+"wifi_ap_rate_bytes_per_second", "Current access point throughput.", ap.RxRate, append(al, l("direction", "rx"))...)
		reg.gauge(ns+"wifi_ap_rate_bytes_per_second", "Current access point throughput.", ap.TxRate, append(al, l("direction", "tx"))...)
	}
	for _, ss := range s.SSIDs {
		sl := []Label{pl, l("ssid", ss.Name)}
		reg.gauge(ns+"wifi_ssid_clients", "Wireless clients per SSID.", float64(ss.NumClients), sl...)
		reg.gauge(ns+"wifi_ssid_rate_bytes_per_second", "Current throughput per SSID.", ss.RxRate, append(sl, l("direction", "rx"))...)
		reg.gauge(ns+"wifi_ssid_rate_bytes_per_second", "Current throughput per SSID.", ss.TxRate, append(sl, l("direction", "tx"))...)
	}

	type key struct{ radio, quality string }
	buckets := make(map[key]int)
	for _, c := range s.Clients {
		buckets[key{c.Radio, signalQuality(c.Signal)}]++
	}
	for k, v := range buckets {
		reg.gauge(ns+"wifi_clients_by_signal", "Wireless clients per radio band and signal quality (excellent >= -55 dBm, good >= -67, fair >= -75, poor below).",
			float64(v), pl, l("radio", k.radio), l("quality", k.quality))
	}

	clients := append([]wifi.ClientInfo(nil), s.Clients...)
	sort.SliceStable(clients, func(i, j int) bool {
		return clients[i].RxRate+clients[i].TxRate > clients[j].RxRate+clients[j].TxRate
	})
	for i, c := range clients {
		if i >= n {
			break
		}
		host := c.Hostname
		if host == "" {
			host = c.MAC
		}
		cl := []Label{pl, l("mac", c.MAC), l("hostname", host), l("ssid", c.SSID), l("ap", c.APName), l("radio", c.Radio)}
		reg.gauge(ns+"wifi_top_client_signal_dbm", "Signal strength of the top-N wireless clients by throughput.", float64(c.Signal), cl...)
		reg.gauge(ns+"wifi_top_client_rate_bytes_per_second", "Throughput of the top-N wireless clients.", c.RxRate, append(cl, l("direction", "rx"))...)
		reg.gauge(ns+"wifi_top_client_rate_bytes_per_second", "Throughput of the top-N wireless clients.", c.TxRate, append(cl, l("direction", "tx"))...)
	}
}

// --- conntrack ---------------------------------------------------------------

func hostStatLabels(h conntrack.HostStat) []Label {
	host := h.Hostname
	if host == "" {
		host = h.IP
	}
	asn := ""
	if h.ASN != 0 {
		asn = strconv.FormatUint(uint64(h.ASN), 10)
	}
	return []Label{l("ip", h.IP), l("hostname", host), l("country", h.Country), l("asn", asn), l("as_org", h.ASOrg)}
}

func emitCounts(reg *registry, name, help, label string, m map[string]int) {
	for k, v := range m {
		if k == "" {
			k = "none"
		}
		reg.gauge(name, help, float64(v), l(label, strings.ToLower(k)))
	}
}

func collectConntrack(reg *registry, t *conntrack.Tracker, n int) {
	if t == nil {
		return
	}
	s := t.GetSummary()
	if s == nil {
		return
	}
	reg.gauge(ns+"conntrack_entries", "Tracked connections.", float64(s.Total))
	reg.gauge(ns+"conntrack_entries_limit", "Maximum tracked connections (nf_conntrack_max).", float64(s.Max))
	reg.gauge(ns+"conntrack_entries_by_family", "Tracked connections per address family.", float64(s.IPv4), l("family", "ipv4"))
	reg.gauge(ns+"conntrack_entries_by_family", "Tracked connections per address family.", float64(s.IPv6), l("family", "ipv6"))
	emitCounts(reg, ns+"conntrack_entries_by_protocol", "Tracked connections per protocol.", "protocol", s.Protocols)
	emitCounts(reg, ns+"conntrack_entries_by_state", "Tracked TCP connections per state.", "state", s.States)
	emitCounts(reg, ns+"conntrack_entries_by_nat_type", "Tracked connections per NAT type.", "nat_type", s.NATTypes)

	for i, h := range s.TopLANClients {
		if i >= n {
			break
		}
		hl := hostStatLabels(h)
		reg.gauge(ns+"conntrack_top_lan_client_connections", "Tracked connections of the top-N LAN clients.", float64(h.Connections), hl...)
		reg.gauge(ns+"conntrack_top_lan_client_bytes", "Bytes accounted to the open connections of the top-N LAN clients.", float64(h.Bytes), hl...)
	}
	for i, h := range s.TopRemoteDestinations {
		if i >= n {
			break
		}
		hl := hostStatLabels(h)
		reg.gauge(ns+"conntrack_top_remote_connections", "Tracked connections to the top-N remote destinations.", float64(h.Connections), hl...)
		reg.gauge(ns+"conntrack_top_remote_bytes", "Bytes accounted to the open connections of the top-N remote destinations.", float64(h.Bytes), hl...)
	}
}

// --- latency -----------------------------------------------------------------

func collectLatency(reg *registry, m *latency.Monitor) {
	if m == nil {
		return
	}
	collectLatencyStatus(reg, m.GetStatus())
}

// collectLatencyStatus exports latency stats. Per-WAN probes carry a "wan"
// label; default-route probes have none, so single-WAN output is unchanged.
func collectLatencyStatus(reg *registry, status []latency.TargetStatus) {
	routable := map[[2]string]bool{}
	for _, t := range status {
		tl := []Label{l("target", t.Name)}
		if t.WAN != "" {
			tl = append(tl, l("wan", t.WAN))
			if t.Default {
				// This WAN carries the default route; no separate
				// default-route series is exported.
				tl = append(tl, l("default", "true"))
			}
			unr := map[string]bool{}
			for _, f := range t.Unroutable {
				unr[f] = true
			}
			fams := 0
			for _, f := range []struct {
				name string
				addr string
			}{{"ipv4", t.IPv4}, {"ipv6", t.IPv6}} {
				if f.addr == "" {
					continue
				}
				fams++
				k := [2]string{t.WAN, f.name}
				if _, ok := routable[k]; !ok {
					routable[k] = !unr[f.name]
					reg.gauge(ns+"latency_wan_routable", "1 if host traffic can be routed via the WAN for the address family (probes run), 0 if no route or ip rule allows it (probes skipped).", boolFloat(!unr[f.name]), l("wan", t.WAN), l("family", f.name))
				}
			}
			if fams > 0 && len(unr) >= fams {
				continue // not probed at all; see latency_wan_routable
			}
		}
		reg.gauge(ns+"latency_target_up", "1 if the latency target answered recently.", boolFloat(t.Alive), tl...)
		for _, p := range []struct {
			probe string
			st    *latency.ProbeStats
		}{{"icmp", t.ICMPStats}, {"https", t.HTTPSStats}} {
			if p.st == nil {
				continue
			}
			pl := append(append([]Label(nil), tl...), l("probe", p.probe))
			ms := func(name, help string, v float64) {
				if v >= 0 {
					reg.gauge(ns+name, help, v/1000, pl...)
				}
			}
			ms("latency_rtt_seconds", "Most recent round-trip time.", p.st.RTT)
			ms("latency_rtt_avg_seconds", "Average round-trip time over the probe window.", p.st.AvgRTT)
			ms("latency_rtt_min_seconds", "Minimum round-trip time over the probe window.", p.st.MinRTT)
			ms("latency_rtt_max_seconds", "Maximum round-trip time over the probe window.", p.st.MaxRTT)
			ms("latency_rtt_p95_seconds", "95th percentile round-trip time over the probe window.", p.st.P95RTT)
			ms("latency_rtt_p99_seconds", "99th percentile round-trip time over the probe window.", p.st.P99RTT)
			ms("latency_jitter_seconds", "Round-trip jitter over the probe window.", p.st.Jitter)
			reg.gauge(ns+"latency_loss_ratio", "Fraction of probes lost over the probe window (0-1).", p.st.LossPct/100, pl...)
		}
	}
}

// --- speed test --------------------------------------------------------------

func collectSpeedtest(reg *registry, t *speedtest.Tester) {
	if t == nil {
		return
	}
	reg.gauge(ns+"speedtest_running", "1 while a speed test is in progress.", boolFloat(t.IsRunning()))
	results := t.GetResults()
	reg.gauge(ns+"speedtest_results", "Speed test results kept in memory since start.", float64(len(results)))
	seen := make(map[string]bool)
	for _, r := range results { // newest first
		iface := r.Interface
		if iface == "" {
			iface = "default"
		}
		if seen[iface] {
			continue
		}
		seen[iface] = true
		il := l("interface", iface)
		reg.gauge(ns+"speedtest_download_bits_per_second", "Latest multi-stream download speed.", r.DownloadMbps*1e6, il)
		reg.gauge(ns+"speedtest_upload_bits_per_second", "Latest multi-stream upload speed.", r.UploadMbps*1e6, il)
		if r.DownloadSingleMbps > 0 {
			reg.gauge(ns+"speedtest_download_single_stream_bits_per_second", "Latest single-stream download speed.", r.DownloadSingleMbps*1e6, il)
		}
		if r.UploadSingleMbps > 0 {
			reg.gauge(ns+"speedtest_upload_single_stream_bits_per_second", "Latest single-stream upload speed.", r.UploadSingleMbps*1e6, il)
		}
		reg.gauge(ns+"speedtest_ping_seconds", "Latest speed test ping.", r.PingMs/1000, il)
		reg.gauge(ns+"speedtest_jitter_seconds", "Latest speed test jitter.", r.JitterMs/1000, il)
		reg.gauge(ns+"speedtest_last_run_timestamp_seconds", "Unix time of the latest speed test.", float64(r.Timestamp)/1000, il)
	}
}

// --- topology ----------------------------------------------------------------

func collectTopology(reg *registry, s *topology.Scanner) {
	if s == nil {
		return
	}
	o := s.GetOverview()
	if o == nil {
		return
	}
	nodes := make(map[string]int)
	for _, n := range o.Nodes {
		nodes[string(n.Type)]++
	}
	links := make(map[string]int)
	for _, lk := range o.Links {
		links[string(lk.Type)]++
	}
	emitCounts(reg, ns+"topology_nodes", "Discovered network devices per type.", "type", nodes)
	emitCounts(reg, ns+"topology_links", "Discovered links per type.", "type", links)
}
