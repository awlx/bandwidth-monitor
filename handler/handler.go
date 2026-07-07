package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"bandwidth-monitor/collector"
	"bandwidth-monitor/conntrack"
	"bandwidth-monitor/debug"
	"bandwidth-monitor/dns"
	"bandwidth-monitor/geoip"
	"bandwidth-monitor/httputil"
	"bandwidth-monitor/latency"
	"bandwidth-monitor/liveactivity"
	"bandwidth-monitor/netutil"
	"bandwidth-monitor/resolver"
	"bandwidth-monitor/speedtest"
	"bandwidth-monitor/talkers"
	"bandwidth-monitor/topology"
	"bandwidth-monitor/wifi"
)

func InterfaceStats(c *collector.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteJSON(w, c.GetAll())
	}
}

// InterfaceHistory serves per-interface rate history. Optional query params narrow the response
// (both are additive — old clients that send neither get the full map exactly as before):
//   - iface: return only this interface's history
//   - since: return only points at or after this Unix-milliseconds timestamp
func InterfaceHistory(c *collector.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		iface := r.URL.Query().Get("iface")
		if len(iface) > 64 {
			http.Error(w, "iface too long", http.StatusBadRequest)
			return
		}
		var since int64
		if s := r.URL.Query().Get("since"); s != "" {
			var err error
			since, err = strconv.ParseInt(s, 10, 64)
			if err != nil || since < 0 {
				http.Error(w, "since must be a Unix-milliseconds timestamp", http.StatusBadRequest)
				return
			}
		}
		httputil.WriteJSON(w, c.GetHistoryFiltered(iface, since))
	}
}

func TopTalkersBandwidth(t *talkers.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteJSON(w, t.TopByBandwidth(10))
	}
}

func TopTalkersVolume(t *talkers.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		httputil.WriteJSON(w, t.TopByVolume(10))
	}
}

// CountryTalkers returns the top IPs (by 24h volume) for a given GeoIP
// country code. Unlike the global Top Talkers lists, this searches every
// known IP rather than just the overall top-10, so it can power a
// "click a country on the world map" drill-down that finds traffic the
// global list wouldn't otherwise surface.
func CountryTalkers(t *talkers.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cc := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("cc")))
		if cc == "" {
			http.Error(w, "cc parameter required", http.StatusBadRequest)
			return
		}
		if len(cc) != 2 {
			http.Error(w, "invalid cc", http.StatusBadRequest)
			return
		}
		httputil.WriteJSON(w, t.TopByCountry(cc, 10))
	}
}

// ASNTalkers returns the local machines (by 24h volume) that have talked to
// a given ASN, powering the "which of my machines talk to this provider"
// drill-down on the ASN breakdown card.
func ASNTalkers(t *talkers.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		asnStr := strings.TrimSpace(r.URL.Query().Get("asn"))
		if asnStr == "" {
			http.Error(w, "asn parameter required", http.StatusBadRequest)
			return
		}
		asn, err := strconv.ParseUint(asnStr, 10, 32)
		if err != nil || asn == 0 {
			http.Error(w, "invalid asn", http.StatusBadRequest)
			return
		}
		httputil.WriteJSON(w, t.TopMachinesForASN(uint(asn), 25))
	}
}

func DNSSummary(dp dns.Provider, dnsRes *resolver.Resolver) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dp == nil {
			httputil.WriteJSONOrNull(w, nil)
			return
		}
		sum := dp.GetSummary()
		if sum != nil && dnsRes != nil {
			for i := range sum.TopClients {
				if name := dnsRes.LookupAddrAsync(sum.TopClients[i].IP); name != "" && name != sum.TopClients[i].IP {
					sum.TopClients[i].Hostname = name
				}
			}
		}
		httputil.WriteJSON(w, sum)
	}
}

func WiFiSummary(wp wifi.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if wp == nil {
			httputil.WriteJSONOrNull(w, nil)
			return
		}
		httputil.WriteJSON(w, wp.GetSummary())
	}
}

func TopologySummary(ts *topology.Scanner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ts == nil {
			httputil.WriteJSONOrNull(w, nil)
			return
		}
		httputil.WriteJSON(w, ts.GetOverview())
	}
}

func ConntrackSummary(ct *conntrack.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ct == nil {
			httputil.WriteJSONOrNull(w, nil)
			return
		}
		httputil.WriteJSON(w, ct.GetSummary())
	}
}

// LatencyStatus returns the current latency monitoring data.
func LatencyStatus(lm *latency.Monitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if lm == nil {
			httputil.WriteJSONOrNull(w, nil)
			return
		}
		httputil.WriteJSON(w, lm.GetStatus())
	}
}

// HostDetail returns detailed information about a specific IP address,
// aggregating data from talkers (bandwidth history), conntrack (active flows),
// DNS (hostname), and GeoIP (country/ASN).
func HostDetail(t *talkers.Tracker, ct *conntrack.Tracker, geoDB *geoip.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := r.URL.Query().Get("ip")
		if ip == "" {
			http.Error(w, "ip parameter required", http.StatusBadRequest)
			return
		}
		if len(ip) > 45 {
			http.Error(w, "invalid ip", http.StatusBadRequest)
			return
		}

		type hostDetail struct {
			IP          string                `json:"ip"`
			Hostname    string                `json:"hostname,omitempty"`
			Country     string                `json:"country,omitempty"`
			CountryName string                `json:"country_name,omitempty"`
			City        string                `json:"city,omitempty"`
			ASN         uint                  `json:"asn,omitempty"`
			ASOrg       string                `json:"as_org,omitempty"`
			TotalBytes  uint64                `json:"total_bytes"`
			RxBytes     uint64                `json:"rx_bytes"`
			TxBytes     uint64                `json:"tx_bytes"`
			Packets     uint64                `json:"packets"`
			RateBytes   float64               `json:"rate_bytes"`
			RxRate      float64               `json:"rx_rate"`
			TxRate      float64               `json:"tx_rate"`
			History     []talkers.BucketPoint `json:"history"`
			Connections []conntrack.Entry     `json:"connections"`
			Timestamp   int64                 `json:"timestamp"`
		}

		detail := hostDetail{
			IP:        ip,
			Timestamp: time.Now().UnixMilli(),
		}

		// Talker data
		if totals := t.HostTotals(ip); totals != nil {
			detail.Hostname = totals.Hostname
			detail.Country = totals.Country
			detail.CountryName = totals.CountryName
			detail.City = totals.City
			detail.ASN = totals.ASN
			detail.ASOrg = totals.ASOrg
			detail.TotalBytes = totals.TotalBytes
			detail.RxBytes = totals.RxBytes
			detail.TxBytes = totals.TxBytes
			detail.Packets = totals.Packets
			detail.RateBytes = totals.RateBytes
			detail.RxRate = totals.RxRate
			detail.TxRate = totals.TxRate
		}

		// GeoIP fallback (in case TalkerStat had no geo data)
		if geoDB != nil && geoDB.Available() && detail.Country == "" {
			if geo := geoDB.Lookup(ip); geo != nil {
				detail.City = geo.City
				if detail.Country == "" {
					detail.Country = geo.Country
					detail.CountryName = geo.CountryName
				}
				if detail.ASN == 0 {
					detail.ASN = geo.ASN
					detail.ASOrg = geo.ASOrg
				}
			}
		}

		// Bandwidth history
		detail.History = t.HostHistory(ip)

		// Conntrack flows
		if ct != nil {
			detail.Connections = ct.HostFlows(ip)
		}

		httputil.WriteJSON(w, detail)
	}
}

// HostDNSLog returns recent DNS queries made by a specific client IP, if the
// configured DNS provider supports per-client query history (currently only
// AdGuard Home does, via dns.ClientQueryLogger). Additive endpoint; existing
// clients that don't call it are unaffected.
func HostDNSLog(dnsProvider dns.Provider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := r.URL.Query().Get("ip")
		if ip == "" {
			http.Error(w, "ip parameter required", http.StatusBadRequest)
			return
		}
		if len(ip) > 45 || net.ParseIP(ip) == nil {
			http.Error(w, "invalid ip", http.StatusBadRequest)
			return
		}

		type response struct {
			Available bool                `json:"available"`
			Queries   []dns.QueryLogEntry `json:"queries"`
		}

		if dnsProvider == nil || !dnsProvider.Available() {
			httputil.WriteJSON(w, response{Available: false})
			return
		}
		logger, ok := dnsProvider.(dns.ClientQueryLogger)
		if !ok {
			httputil.WriteJSON(w, response{Available: false})
			return
		}

		entries, err := logger.QueryLog(ip, 50)
		if err != nil {
			log.Printf("host dns log: %v", err)
			httputil.WriteJSON(w, response{Available: false})
			return
		}
		if entries == nil {
			entries = []dns.QueryLogEntry{}
		}
		httputil.WriteJSON(w, response{Available: true, Queries: entries})
	}
}

// MenuBarSummary returns a compact JSON snapshot for menu-bar widgets.
func MenuBarSummary(c *collector.Collector, t *talkers.Tracker, dp dns.Provider, wp wifi.Provider, ctr *conntrack.Tracker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type ifaceBrief struct {
			Name   string   `json:"name"`
			Type   string   `json:"type"`
			Addrs  []string `json:"addrs,omitempty"`
			WAN    bool     `json:"wan,omitempty"`
			RxRate float64  `json:"rx_rate"`
			TxRate float64  `json:"tx_rate"`
			State  string   `json:"state"`
		}
		type dnsBrief struct {
			Provider     string  `json:"provider_name"`
			TotalQueries int     `json:"total_queries"`
			Blocked      int     `json:"blocked"`
			BlockPct     float64 `json:"block_pct"`
			LatencyMs    float64 `json:"latency_ms"`
		}
		type wifiBrief struct {
			Provider string `json:"provider_name"`
			APs      int    `json:"aps"`
			Clients  int    `json:"clients"`
		}
		type natBrief struct {
			Total    int     `json:"total"`
			Max      int     `json:"max"`
			UsagePct float64 `json:"usage_pct"`
			IPv4     int     `json:"ipv4"`
			IPv6     int     `json:"ipv6"`
			SNAT     int     `json:"snat"`
			DNAT     int     `json:"dnat"`
		}
		type summary struct {
			App        string       `json:"app"`
			Interfaces []ifaceBrief `json:"interfaces"`
			VPN        bool         `json:"vpn"`
			VPNIface   string       `json:"vpn_iface,omitempty"`
			DNS        *dnsBrief    `json:"dns,omitempty"`
			WiFi       *wifiBrief   `json:"wifi,omitempty"`
			NAT        *natBrief    `json:"nat,omitempty"`
			Timestamp  int64        `json:"timestamp"`
		}

		var out summary
		out.App = "bandwidth-monitor"
		out.Timestamp = time.Now().UnixMilli()

		for _, iface := range c.GetAll() {
			ib := ifaceBrief{
				Name:   iface.Name,
				Type:   iface.IfaceType,
				Addrs:  iface.Addrs,
				WAN:    iface.WAN,
				RxRate: iface.RxRate,
				TxRate: iface.TxRate,
				State:  iface.OperState,
			}
			out.Interfaces = append(out.Interfaces, ib)
			if iface.VPNRouting {
				out.VPN = true
				out.VPNIface = iface.Name
			}
		}
		if dp != nil {
			if ds := dp.GetSummary(); ds != nil {
				out.DNS = &dnsBrief{
					Provider:     ds.ProviderName,
					TotalQueries: ds.TotalQueries,
					Blocked:      ds.BlockedTotal,
					BlockPct:     ds.BlockedPercent,
					LatencyMs:    ds.AvgLatencyMs,
				}
			}
		}
		if wp != nil {
			if ws := wp.GetSummary(); ws != nil {
				totalClients := 0
				for _, ap := range ws.APs {
					totalClients += ap.NumClients
				}
				out.WiFi = &wifiBrief{
					Provider: ws.ProviderName,
					APs:      len(ws.APs),
					Clients:  totalClients,
				}
			}
		}
		if ctr != nil {
			if ns := ctr.GetSummary(); ns != nil {
				out.NAT = &natBrief{
					Total:    ns.Total,
					Max:      ns.Max,
					UsagePct: ns.UsagePct,
					IPv4:     ns.IPv4,
					IPv6:     ns.IPv6,
					SNAT:     ns.NATTypes["snat"] + ns.NATTypes["both"],
					DNAT:     ns.NATTypes["dnat"] + ns.NATTypes["both"],
				}
			}
		}

		httputil.WriteJSON(w, out)
	}
}

// SpeedTestRun triggers a new speed test and streams progress as SSE. An
// optional "iface" query param binds the test to a specific WAN interface
// (multi-WAN setups) via SO_BINDTODEVICE instead of the default route; it
// must name one of the currently-known WAN interfaces reported by the
// collector, to prevent binding to arbitrary/unintended interfaces from
// untrusted input. Omitting it preserves the original single-WAN behavior.
func SpeedTestRun(st *speedtest.Tester, c *collector.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}

		iface := r.URL.Query().Get("iface")
		if iface != "" && !isKnownWANInterface(c, iface) {
			http.Error(w, "unknown WAN interface", http.StatusBadRequest)
			return
		}

		ch := st.Run(iface)
		if ch == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(map[string]string{"error": "test already running"})
			return
		}

		httputil.StreamChannel(w, ch)
	}
}

// isKnownWANInterface reports whether name matches one of the interfaces
// the collector currently reports as a WAN uplink.
func isKnownWANInterface(c *collector.Collector, name string) bool {
	if c == nil {
		return false
	}
	for _, iface := range c.GetAll() {
		if iface.WAN && iface.Name == name {
			return true
		}
	}
	return false
}

// SpeedTestInterfaces lists the currently-known WAN interfaces that a speed
// test can be bound to, for a multi-WAN interface picker in the UI.
func SpeedTestInterfaces(c *collector.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		type wanIface struct {
			Name  string   `json:"name"`
			Addrs []string `json:"addrs,omitempty"`
		}
		out := []wanIface{}
		for _, iface := range c.GetAll() {
			if iface.WAN {
				out = append(out, wanIface{Name: iface.Name, Addrs: iface.Addrs})
			}
		}
		httputil.WriteJSON(w, map[string]interface{}{"interfaces": out})
	}
}

// SpeedTestResults returns the history of speed test results.
func SpeedTestResults(st *speedtest.Tester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := st.GetResults()
		response := map[string]interface{}{
			"running": st.IsRunning(),
			"results": results,
		}
		httputil.WriteJSON(w, response)
	}
}

// DebugTraceroute runs a native ICMP traceroute and streams progress as SSE.
// An optional "iface" query param binds the traceroute to a specific WAN
// interface (same validation/behavior as SpeedTestRun's iface param).
func DebugTraceroute(dns *resolver.Resolver, c *collector.Collector) http.HandlerFunc {
	var sf httputil.SingleFlight

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}

		target := r.URL.Query().Get("target")
		if target == "" {
			http.Error(w, "target parameter required", http.StatusBadRequest)
			return
		}

		// Validate target: only allow hostnames and IPs, max length
		if len(target) > 253 {
			http.Error(w, "target too long", http.StatusBadRequest)
			return
		}

		iface := r.URL.Query().Get("iface")
		if iface != "" && !isKnownWANInterface(c, iface) {
			http.Error(w, "unknown WAN interface", http.StatusBadRequest)
			return
		}

		// Rate limit: only one traceroute at a time
		if !sf.TryAcquire(w, "traceroute") {
			return
		}
		defer sf.Release()

		countStr := r.URL.Query().Get("count")
		count := 20
		if countStr != "" {
			if c, err := strconv.Atoi(countStr); err == nil && c > 0 && c <= 100 {
				count = c
			}
		}

		maxTTLStr := r.URL.Query().Get("maxttl")
		maxTTL := 30
		if maxTTLStr != "" {
			if m, err := strconv.Atoi(maxTTLStr); err == nil && m > 0 && m <= 64 {
				maxTTL = m
			}
		}

		ch := debug.RunTraceroute(target, count, maxTTL, dns, iface)
		httputil.StreamChannel(w, ch)
	}
}

// DebugMTU performs a path MTU discovery to a target and streams progress as
// SSE. An optional "iface" query param binds the test to a specific WAN
// interface (same validation/behavior as SpeedTestRun's iface param).
func DebugMTU(c *collector.Collector) http.HandlerFunc {
	var sf httputil.SingleFlight

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}

		target := r.URL.Query().Get("target")
		if target == "" {
			http.Error(w, "target parameter required", http.StatusBadRequest)
			return
		}

		if len(target) > 253 {
			http.Error(w, "target too long", http.StatusBadRequest)
			return
		}

		iface := r.URL.Query().Get("iface")
		if iface != "" && !isKnownWANInterface(c, iface) {
			http.Error(w, "unknown WAN interface", http.StatusBadRequest)
			return
		}

		// Rate limit: only one MTU test at a time
		if !sf.TryAcquire(w, "MTU test") {
			return
		}
		defer sf.Release()

		ch := debug.RunMTUDiscovery(target, iface)
		httputil.StreamChannel(w, ch)
	}
}

// DebugDNS runs DNS checks against multiple servers.
func DebugDNS() http.HandlerFunc {
	var mu sync.Mutex
	lastRun := time.Time{}

	return func(w http.ResponseWriter, r *http.Request) {
		domain := r.URL.Query().Get("domain")
		if domain == "" {
			http.Error(w, "domain parameter required", http.StatusBadRequest)
			return
		}

		// Validate domain: max length, no spaces/special chars
		if len(domain) > 253 {
			http.Error(w, "domain too long", http.StatusBadRequest)
			return
		}

		// Rate limit: 1 query per 2 seconds
		mu.Lock()
		if time.Since(lastRun) < 2*time.Second {
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{"error": "rate limited, try again shortly"})
			return
		}
		lastRun = time.Now()
		mu.Unlock()

		qtype := r.URL.Query().Get("type")
		if qtype == "" {
			qtype = "A"
		}

		result := debug.RunDNSCheck(domain, qtype)

		httputil.WriteJSON(w, result)
	}
}

// DebugPublicIP reports the public (external) IP address as seen from a
// specific WAN interface (or the default route if "iface" is omitted). This
// is the multi-WAN equivalent of the CGNAT fallback lookup used elsewhere
// (fetchExternalIP), exposed as a first-class debug tool so users can
// confirm which public IP each uplink actually egresses with — handy for
// spotting misconfigured policy routing (e.g. a WAN silently going out the
// wrong interface).
func DebugPublicIP(c *collector.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		iface := r.URL.Query().Get("iface")
		if iface != "" && !isKnownWANInterface(c, iface) {
			http.Error(w, "unknown WAN interface", http.StatusBadRequest)
			return
		}

		client := &http.Client{
			Timeout:   5 * time.Second,
			Transport: httputil.WrapTransport(&http.Transport{DialContext: netutil.DialerForInterface(iface).DialContext}),
		}

		resp, err := client.Get("https://ip.ffmuc.net")
		if err != nil {
			httputil.WriteJSON(w, map[string]interface{}{"error": sanitizeDialError(err, iface)})
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
		if err != nil {
			httputil.WriteJSON(w, map[string]interface{}{"error": "failed to read response"})
			return
		}
		ip := strings.TrimSpace(string(body))
		if net.ParseIP(ip) == nil {
			httputil.WriteJSON(w, map[string]interface{}{"error": "unexpected response from IP lookup service"})
			return
		}

		resp2 := map[string]interface{}{"ip": ip}
		if iface != "" {
			resp2["interface"] = iface
		}
		httputil.WriteJSON(w, resp2)
	}
}

// DebugTCPCheck attempts a TCP connection to a "target" of the form
// host:port, optionally bound to a specific WAN interface via "iface", and
// reports whether the connection succeeded plus how long it took. Useful
// for checking that a specific WAN can actually reach a given
// host/port (e.g. testing a port-forward or verifying a specific uplink
// isn't blocking a given port).
func DebugTCPCheck(c *collector.Collector) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target := r.URL.Query().Get("target")
		if target == "" {
			http.Error(w, "target parameter required", http.StatusBadRequest)
			return
		}
		if len(target) > 253 {
			http.Error(w, "target too long", http.StatusBadRequest)
			return
		}
		host, port, err := net.SplitHostPort(target)
		if err != nil || host == "" || port == "" {
			http.Error(w, "target must be host:port", http.StatusBadRequest)
			return
		}
		if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
			http.Error(w, "invalid port", http.StatusBadRequest)
			return
		}

		iface := r.URL.Query().Get("iface")
		if iface != "" && !isKnownWANInterface(c, iface) {
			http.Error(w, "unknown WAN interface", http.StatusBadRequest)
			return
		}

		dialer := netutil.DialerForInterface(iface)
		start := time.Now()
		conn, err := dialer.DialContext(r.Context(), "tcp", target)
		elapsed := time.Since(start)

		result := map[string]interface{}{
			"target":     target,
			"elapsed_ms": float64(elapsed.Microseconds()) / 1000.0,
		}
		if iface != "" {
			result["interface"] = iface
		}
		if err != nil {
			result["success"] = false
			result["error"] = sanitizeDialError(err, iface)
		} else {
			conn.Close()
			result["success"] = true
		}
		httputil.WriteJSON(w, result)
	}
}

// sanitizeDialError produces a user-facing error message for a failed dial,
// adding a hint when an interface-bound dial fails in a way that suggests
// the interface itself may lack a working route (rather than the target
// simply being unreachable).
func sanitizeDialError(err error, iface string) string {
	msg := err.Error()
	if iface != "" {
		return fmt.Sprintf("connection failed via interface %s (check it has its own working route/policy routing): %s", iface, msg)
	}
	return fmt.Sprintf("connection failed: %s", msg)
}

// originResolver determines the WAN's geographic country code for the map
// origin point.  It caches the result and refreshes periodically.
type originGeo struct {
	Country   string  `json:"country"`
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
}

type originResolver struct {
	mu       sync.RWMutex
	origin   *originGeo
	resolved bool // true after first resolve attempt (even if result is nil)
	last     time.Time
	ttl      time.Duration
	geoDB    *geoip.DB
}

func newOriginResolver(geoDB *geoip.DB) *originResolver {
	return &originResolver{
		geoDB: geoDB,
		ttl:   10 * time.Minute,
	}
}

// resolve determines the origin location from the WAN interface IPs.
// Priority: global IPv4 > global IPv6 > ip.ffmuc.net fallback.
func (o *originResolver) resolve(c *collector.Collector) *originGeo {
	o.mu.RLock()
	if o.resolved && time.Since(o.last) < o.ttl {
		og := o.origin
		o.mu.RUnlock()
		return og
	}
	o.mu.RUnlock()

	og := o.doResolve(c)

	o.mu.Lock()
	o.origin = og
	o.resolved = true
	o.last = time.Now()
	o.mu.Unlock()

	return og
}

func (o *originResolver) doResolve(c *collector.Collector) *originGeo {
	if o.geoDB == nil || !o.geoDB.Available() {
		return nil
	}

	// Find WAN interface IPs
	var wanIPv4, wanIPv6 string
	for _, iface := range c.GetAll() {
		if !iface.WAN {
			continue
		}
		for _, addrStr := range iface.Addrs {
			ip, _, err := net.ParseCIDR(addrStr)
			if err != nil {
				continue
			}
			if ip.To4() != nil && wanIPv4 == "" {
				if netutil.IsGlobalUnicast(ip) {
					wanIPv4 = ip.String()
				}
			} else if ip.To4() == nil && wanIPv6 == "" {
				if netutil.IsGlobalUnicast(ip) {
					wanIPv6 = ip.String()
				}
			}
		}
	}

	// Try IPv4 first, then IPv6
	for _, ipStr := range []string{wanIPv4, wanIPv6} {
		if ipStr == "" {
			continue
		}
		if r := o.geoDB.Lookup(ipStr); r != nil && r.Country != "" {
			log.Printf("geo origin: %s -> %s (%.4f, %.4f)", ipStr, r.Country, r.Latitude, r.Longitude)
			return &originGeo{Country: r.Country, Latitude: r.Latitude, Longitude: r.Longitude}
		}
	}

	// Fallback: no globally routable WAN IP found, query ip.ffmuc.net
	log.Printf("geo origin: no globally routable WAN IP, trying ip.ffmuc.net")
	if extIP := fetchExternalIP(); extIP != "" {
		if r := o.geoDB.Lookup(extIP); r != nil && r.Country != "" {
			log.Printf("geo origin: ip.ffmuc.net %s -> %s (%.4f, %.4f)", extIP, r.Country, r.Latitude, r.Longitude)
			return &originGeo{Country: r.Country, Latitude: r.Latitude, Longitude: r.Longitude}
		}
	}

	return nil
}

// externalIPClient is reused across calls to fetchExternalIP so we don't pay
// for a new connection pool on every fallback lookup (this path only runs
// when no globally-routable WAN IP was found, at most once per TTL refresh).
var externalIPClient = &http.Client{Timeout: 3 * time.Second, Transport: httputil.WrapTransport(nil)}

// fetchExternalIP queries ip.ffmuc.net to get the public IP when all
// WAN addresses are behind CGNAT or not globally routable.
func fetchExternalIP() string {
	resp, err := externalIPClient.Get("https://ip.ffmuc.net")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) == nil {
		return ""
	}
	return ip
}

// buildPayload assembles the JSON payload sent over the SSE stream.
func buildPayload(c *collector.Collector, t *talkers.Tracker, dp dns.Provider, wp wifi.Provider, ct *conntrack.Tracker, lm *latency.Monitor, ts *topology.Scanner, dnsRes *resolver.Resolver, origin *originResolver) map[string]interface{} {
	geo := t.GetGeoBreakdown()
	var ov *topology.Overview
	if ts != nil {
		ov = ts.GetOverview()
	}

	var topologyBandwidth []talkers.TalkerStat
	if ov != nil {
		ips := make([]string, 0, len(ov.Nodes)*2)
		for _, n := range ov.Nodes {
			for _, ip := range n.IPs {
				if ip == "" {
					continue
				}
				ips = append(ips, ip)
			}
		}
		topologyBandwidth = t.BandwidthForIPs(ips)
	}

	payload := map[string]interface{}{
		"interfaces":          c.GetAll(),
		"sparklines":          c.GetSparklines(5*time.Minute, 50),
		"protocols":           t.GetProtocolBreakdown(),
		"ip_versions":         t.GetIPVersionBreakdown(),
		"countries":           geo.Countries,
		"asns":                geo.ASNs,
		"top_bandwidth":       t.TopByBandwidth(10),
		"topology_bandwidth":  topologyBandwidth,
		"top_volume":          t.TopByVolume(10),
		"unique_ips":          t.UniqueIPs(),
		"uptime_secs":         readUptime(),
		"process_uptime_secs": time.Since(processStartTime).Seconds(),
		"load_avg":            readLoadAvg(),
		"processes":           func() map[string]int { r, t := readProcessCount(); return map[string]int{"running": r, "total": t} }(),
		"timestamp":           time.Now().UnixMilli(),
	}
	if origin != nil {
		if og := origin.resolve(c); og != nil {
			payload["origin_country"] = og.Country
			if og.Latitude != 0 || og.Longitude != 0 {
				payload["origin_lat"] = og.Latitude
				payload["origin_lon"] = og.Longitude
			}
		}
	}
	if dp != nil {
		sum := dp.GetSummary()
		if sum != nil && dnsRes != nil {
			for i := range sum.TopClients {
				if name := dnsRes.LookupAddrAsync(sum.TopClients[i].IP); name != "" && name != sum.TopClients[i].IP {
					sum.TopClients[i].Hostname = name
				}
			}
		}
		payload["dns"] = sum
	}
	if wp != nil {
		payload["wifi"] = wp.GetSummary()
	}
	if ct != nil {
		if s := ct.GetSummary(); s != nil {
			payload["conntrack"] = s
		}
	}
	if lm != nil {
		payload["latency"] = lm.GetStatus()
	}
	if ov != nil {
		payload["topology"] = ov
	}
	return payload
}

// SSE streams a lightweight JSON payload every second using Server-Sent Events.
// SSE uses plain HTTP — no upgrade handshake, no per-origin connection pool
// issues, and built-in auto-reconnect in the browser's EventSource API.
//
// A dedicated writer goroutine drains a 1-slot channel.  If the client is
// backed up (e.g. hibernating laptop, congested link), only the most recent
// payload is kept — preventing kernel send-buffer buildup (same backpressure
// logic that PR #18 added to the old WebSocket handler).
func SSE(c *collector.Collector, t *talkers.Tracker, dp dns.Provider, wp wifi.Provider, ct *conntrack.Tracker, lm *latency.Monitor, ts *topology.Scanner, dnsRes *resolver.Resolver, geoDB *geoip.DB) http.HandlerFunc {
	origin := newOriginResolver(geoDB)
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		// Non-blocking write channel: the ticker produces payloads and a
		// dedicated writer goroutine drains them.  If the client is backed
		// up, only the most recent payload is kept.
		sendCh := make(chan []byte, 1)

		// Writer goroutine — serialises all writes to the response.
		writerDone := make(chan struct{})
		go func() {
			defer close(writerDone)
			for data := range sendCh {
				if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
					return
				}
				flusher.Flush()
			}
		}()

		// Send initial payload immediately.
		data, err := json.Marshal(buildPayload(c, t, dp, wp, ct, lm, ts, dnsRes, origin))
		if err != nil {
			close(sendCh)
			return
		}
		sendCh <- data

		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-r.Context().Done():
				close(sendCh)
				<-writerDone // wait for writer to finish before ResponseWriter is invalidated
				return
			case <-writerDone:
				return
			case <-ticker.C:
				data, err := json.Marshal(buildPayload(c, t, dp, wp, ct, lm, ts, dnsRes, origin))
				if err != nil {
					continue
				}
				// Non-blocking send: drop the old message if backed up
				select {
				case sendCh <- data:
				default:
					// Channel full — drain stale message, enqueue fresh one
					select {
					case <-sendCh:
					default:
					}
					sendCh <- data
				}
			}
		}
	}
}

// processStartTime records when this handler package was initialised, used to compute the
// running process's own uptime (distinct from the host's system uptime).
var processStartTime = time.Now()

// readUptime reads the system uptime from /proc/uptime in seconds.
func readUptime() float64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	parts := strings.Fields(string(data))
	if len(parts) < 1 {
		return 0
	}
	v, _ := strconv.ParseFloat(parts[0], 64)
	return v
}

// readLoadAvg reads the 1/5/15 minute load averages from /proc/loadavg.
func readLoadAvg() [3]float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return [3]float64{}
	}
	parts := strings.Fields(string(data))
	if len(parts) < 3 {
		return [3]float64{}
	}
	var la [3]float64
	la[0], _ = strconv.ParseFloat(parts[0], 64)
	la[1], _ = strconv.ParseFloat(parts[1], 64)
	la[2], _ = strconv.ParseFloat(parts[2], 64)
	return la
}

// readProcessCount reads the running/total process count from /proc/loadavg.
func readProcessCount() (running, total int) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, 0
	}
	parts := strings.Fields(string(data))
	if len(parts) < 4 {
		return 0, 0
	}
	// Format: "running/total"
	rt := strings.SplitN(parts[3], "/", 2)
	if len(rt) == 2 {
		running, _ = strconv.Atoi(rt[0])
		total, _ = strconv.Atoi(rt[1])
	}
	return
}

// LiveActivityRegister records an iOS Live Activity push token so the server can drive updates via
// APNs. Body: {"token":"<hex>","interface":"eth0","environment":"production|sandbox"}.
func LiveActivityRegister(m *liveactivity.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
		var body struct {
			Token       string `json:"token"`
			Interface   string `json:"interface"`
			Environment string `json:"environment"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" || len(body.Token) > 200 {
			http.Error(w, "invalid body: expected {token, interface, environment}", http.StatusBadRequest)
			return
		}
		if !m.Register(body.Token, body.Interface, body.Environment) {
			http.Error(w, "registry at capacity", http.StatusServiceUnavailable)
			return
		}
		httputil.WriteJSON(w, map[string]string{"status": "ok"})
	}
}
