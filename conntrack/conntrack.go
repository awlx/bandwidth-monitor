package conntrack

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"time"

	"bandwidth-monitor/geoip"
	"bandwidth-monitor/netutil"
	"bandwidth-monitor/poller"
	"bandwidth-monitor/resolver"
)

const pollInterval = 5 * time.Second

// protoName maps IANA protocol numbers to names.
var protoName = map[uint8]string{
	1:   "ICMP",
	6:   "TCP",
	17:  "UDP",
	33:  "DCCP",
	47:  "GRE",
	58:  "ICMPv6",
	132: "SCTP",
}

// Entry represents a single conntrack entry.
type Entry struct {
	Family   string `json:"family"`   // ipv4 or ipv6
	Protocol string `json:"protocol"` // tcp, udp, icmp, etc.
	State    string `json:"state"`    // ESTABLISHED, TIME_WAIT, etc. (TCP only)
	TTL      int    `json:"ttl"`

	// Original direction
	OrigSrc     string `json:"orig_src"`
	OrigDst     string `json:"orig_dst"`
	OrigSPort   string `json:"orig_sport,omitempty"`
	OrigDPort   string `json:"orig_dport,omitempty"`
	OrigSrcHost string `json:"orig_src_host,omitempty"`
	OrigSrcGeo  string `json:"orig_src_geo,omitempty"`
	OrigSrcCity string `json:"orig_src_city,omitempty"`
	OrigSrcASN  string `json:"orig_src_asn,omitempty"`
	OrigDstHost string `json:"orig_dst_host,omitempty"`
	OrigDstGeo  string `json:"orig_dst_geo,omitempty"`
	OrigDstCity string `json:"orig_dst_city,omitempty"`
	OrigDstASN  string `json:"orig_dst_asn,omitempty"`

	// Reply direction
	ReplSrc   string `json:"repl_src"`
	ReplDst   string `json:"repl_dst"`
	ReplSPort string `json:"repl_sport,omitempty"`
	ReplDPort string `json:"repl_dport,omitempty"`

	NATType string `json:"nat_type"` // snat, dnat, both, none
	Assured bool   `json:"assured"`
	Bytes   uint64 `json:"bytes,omitempty"`
	Packets uint64 `json:"packets,omitempty"`
}

// HostStat aggregates connections per host.
type HostStat struct {
	IP          string `json:"ip"`
	Hostname    string `json:"hostname,omitempty"`
	Connections int    `json:"connections"`
	Bytes       uint64 `json:"bytes"`
	NATType     string `json:"nat_type,omitempty"`
	Country     string `json:"country,omitempty"`
	CountryName string `json:"country_name,omitempty"`
	City        string `json:"city,omitempty"`
	ASN         uint   `json:"asn,omitempty"`
	ASOrg       string `json:"as_org,omitempty"`
}

// SetGeo implements geoip.GeoFields.
func (h *HostStat) SetGeo(country, countryName, city string, lat, lon float64, asn uint, asOrg string) {
	h.Country = country
	h.CountryName = countryName
	h.City = city
	h.ASN = asn
	h.ASOrg = asOrg
}

// Summary holds aggregated conntrack data.
type Summary struct {
	Total    int     `json:"total"`
	Max      int     `json:"max"`
	IPv4     int     `json:"ipv4"`
	IPv6     int     `json:"ipv6"`
	UsagePct float64 `json:"usage_pct"`

	Protocols map[string]int `json:"protocols"`
	States    map[string]int `json:"states"`
	NATTypes  map[string]int `json:"nat_types"`

	TopLANClients         []HostStat `json:"top_lan_clients"`
	TopRemoteDestinations []HostStat `json:"top_remote_destinations"`

	IPv4Entries []Entry `json:"ipv4_entries"`
	IPv6Entries []Entry `json:"ipv6_entries"`

	Timestamp int64 `json:"timestamp"`
}

// Tracker periodically queries the kernel connection-state table (Linux
// conntrack via netlink, FreeBSD pf states via pfctl) and provides a summary.
type Tracker struct {
	mu        sync.RWMutex
	summary   *Summary
	localNets []*net.IPNet
	available bool
	poller.Runner
	geoDB *geoip.DB
	dns   *resolver.Resolver
	src   *stateSource
}

// New creates a new conntrack Tracker.
// localNets defines which IPs are considered local/LAN (used to split
// top sources vs top destinations into LAN clients vs remote hosts).
func New(localNets []*net.IPNet, geoDB *geoip.DB, dns *resolver.Resolver) *Tracker {
	t := &Tracker{
		localNets: localNets,
		geoDB:     geoDB,
		dns:       dns,
		src:       newStateSource(),
	}
	t.Runner.Init()
	return t
}

// Run probes the platform state table and then starts the periodic polling
// loop. If the state table is not available it logs once and returns.
func (t *Tracker) Run() {
	if !t.src.probe() {
		return
	}
	t.available = true
	t.Runner.Run(pollInterval, t.poll)
}

// Stop terminates the polling loop.
func (t *Tracker) Stop() {
	t.Runner.Stop()
	t.src.close()
}

// GetSummary returns the latest conntrack summary (nil if unavailable).
func (t *Tracker) GetSummary() *Summary {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.summary
}

// HostFlows returns all conntrack entries involving the given IP address.
func (t *Tracker) HostFlows(ip string) []Entry {
	s := t.GetSummary()
	if s == nil {
		return nil
	}
	var result []Entry
	for _, entries := range [][]Entry{s.IPv4Entries, s.IPv6Entries} {
		for _, e := range entries {
			if e.OrigSrc == ip || e.OrigDst == ip || e.ReplSrc == ip || e.ReplDst == ip {
				result = append(result, e)
			}
		}
	}
	return result
}

func (t *Tracker) poll() {
	if !t.available {
		return
	}

	s := &Summary{
		Protocols: make(map[string]int),
		States:    make(map[string]int),
		NATTypes:  make(map[string]int),
		Timestamp: time.Now().UnixMilli(),
	}

	srcCount := make(map[string]int)    // LAN clients (local sources)
	dstCount := make(map[string]int)    // remote destinations (non-local destinations)
	srcBytes := make(map[string]uint64) // bytes per LAN client
	dstBytes := make(map[string]uint64) // bytes per remote destination

	var ipv4Entries, ipv6Entries []Entry

	count, max, ok := t.src.dump(func(e Entry) {
		if e.OrigSrc == "" {
			return
		}

		switch e.Family {
		case "ipv4":
			s.IPv4++
			ipv4Entries = append(ipv4Entries, e)
		case "ipv6":
			s.IPv6++
			ipv6Entries = append(ipv6Entries, e)
		}

		s.Protocols[e.Protocol]++
		if e.State != "" {
			s.States[e.State]++
		}
		s.NATTypes[e.NATType]++

		// Classify: local sources → LAN clients, non-local destinations → remote hosts
		if netutil.IsLocalStr(e.OrigSrc, t.localNets) {
			srcCount[e.OrigSrc]++
			srcBytes[e.OrigSrc] += e.Bytes
		}
		if !netutil.IsLocalStr(e.OrigDst, t.localNets) && !net.ParseIP(e.OrigDst).IsLoopback() {
			dstCount[e.OrigDst]++
			dstBytes[e.OrigDst] += e.Bytes
		}
	})
	if !ok {
		return
	}
	s.Total = count
	s.Max = max
	if max > 0 {
		s.UsagePct = float64(count) / float64(max) * 100
	}

	s.TopLANClients = topHosts(srcCount, srcBytes, 20)
	s.TopRemoteDestinations = topHosts(dstCount, dstBytes, 20)

	// Enrich top hosts with reverse DNS and GeoIP (outside the hot path).
	t.enrichHosts(s.TopLANClients)
	t.enrichHosts(s.TopRemoteDestinations)

	const maxEntries = 200
	// Sort by bytes (most traffic first), fall back to packets, then TTL.
	entryLess := func(a, b Entry) bool {
		if a.Bytes != b.Bytes {
			return a.Bytes > b.Bytes
		}
		if a.Packets != b.Packets {
			return a.Packets > b.Packets
		}
		return a.TTL > b.TTL
	}
	sort.Slice(ipv4Entries, func(i, j int) bool { return entryLess(ipv4Entries[i], ipv4Entries[j]) })
	sort.Slice(ipv6Entries, func(i, j int) bool { return entryLess(ipv6Entries[i], ipv6Entries[j]) })
	if len(ipv4Entries) > maxEntries {
		ipv4Entries = ipv4Entries[:maxEntries]
	}
	if len(ipv6Entries) > maxEntries {
		ipv6Entries = ipv6Entries[:maxEntries]
	}

	// Enrich the capped entry lists with reverse DNS + GeoIP.
	// Uses the shared async resolver (cache-first) and GeoIP cache.
	t.enrichEntries(ipv4Entries)
	t.enrichEntries(ipv6Entries)

	s.IPv4Entries = ipv4Entries
	s.IPv6Entries = ipv6Entries

	t.mu.Lock()
	t.summary = s
	t.mu.Unlock()
}

// detectNAT determines the NAT type by comparing the original and reply tuples.
func detectNAT(e Entry) string {
	snat := e.ReplDst != "" && e.ReplDst != e.OrigSrc
	dnat := e.ReplSrc != "" && e.ReplSrc != e.OrigDst

	switch {
	case snat && dnat:
		return "both"
	case snat:
		return "snat"
	case dnat:
		return "dnat"
	default:
		return "none"
	}
}

func topHosts(counts map[string]int, bytes map[string]uint64, n int) []HostStat {
	list := make([]HostStat, 0, len(counts))
	for ip, c := range counts {
		list = append(list, HostStat{IP: ip, Connections: c, Bytes: bytes[ip]})
	}
	// Sort by bytes descending, then by connections as tiebreaker.
	sort.Slice(list, func(i, j int) bool {
		if list[i].Bytes != list[j].Bytes {
			return list[i].Bytes > list[j].Bytes
		}
		return list[i].Connections > list[j].Connections
	})
	if len(list) > n {
		list = list[:n]
	}
	return list
}

// enrichHosts adds reverse DNS hostnames and GeoIP data to a host list.
func (t *Tracker) enrichHosts(hosts []HostStat) {
	for i := range hosts {
		ip := hosts[i].IP

		// Reverse DNS via shared resolver (TTL-based cache).
		if t.dns != nil {
			hosts[i].Hostname = t.dns.LookupAddr(ip)
		}

		// GeoIP
		t.geoDB.Enrich(ip, &hosts[i])
	}
}

// enrichEntries adds reverse DNS hostnames and GeoIP to orig_src and orig_dst.
// Uses async resolver (returns cached or raw IP) and GeoIP cache.
func (t *Tracker) enrichEntries(entries []Entry) {
	for i := range entries {
		e := &entries[i]

		// Reverse DNS for original source and destination
		if t.dns != nil {
			e.OrigSrcHost = t.dns.LookupAddrAsync(e.OrigSrc)
			if e.OrigSrcHost == e.OrigSrc {
				e.OrigSrcHost = ""
			}
			e.OrigDstHost = t.dns.LookupAddrAsync(e.OrigDst)
			if e.OrigDstHost == e.OrigDst {
				e.OrigDstHost = ""
			}
		}

		// GeoIP for original source and destination
		if t.geoDB != nil && t.geoDB.Available() {
			if geo := t.geoDB.Lookup(e.OrigSrc); geo != nil {
				if geo.Country != "" {
					e.OrigSrcGeo = geo.Country
				}
				e.OrigSrcCity = geo.City
				if geo.ASOrg != "" {
					e.OrigSrcASN = fmt.Sprintf("AS%d %s", geo.ASN, geo.ASOrg)
				}
			}
			if geo := t.geoDB.Lookup(e.OrigDst); geo != nil {
				if geo.Country != "" {
					e.OrigDstGeo = geo.Country
				}
				e.OrigDstCity = geo.City
				if geo.ASOrg != "" {
					e.OrigDstASN = fmt.Sprintf("AS%d %s", geo.ASN, geo.ASOrg)
				}
			}
		}
	}
}
