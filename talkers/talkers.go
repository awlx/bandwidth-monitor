package talkers

import (
	"fmt"
	"log"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"bandwidth-monitor/geoip"
	"bandwidth-monitor/packets"
	"bandwidth-monitor/resolver"

	"golang.org/x/sys/unix"
)

const (
	snapshotLen int32         = 128
	capTimeout  time.Duration = 100 * time.Millisecond
	bucketSize                = 1 * time.Minute
	maxAge                    = 24 * time.Hour
)

type TalkerKey struct {
	IP string `json:"ip"`
}

type TalkerStat struct {
	IP          string  `json:"ip"`
	Hostname    string  `json:"hostname"`
	Country     string  `json:"country,omitempty"`
	CountryName string  `json:"country_name,omitempty"`
	ASN         uint    `json:"asn,omitempty"`
	ASOrg       string  `json:"as_org,omitempty"`
	TotalBytes  uint64  `json:"total_bytes"`
	RxBytes     uint64  `json:"rx_bytes"`
	TxBytes     uint64  `json:"tx_bytes"`
	RateBytes   float64 `json:"rate_bytes"`
	RxRate      float64 `json:"rx_rate"`
	TxRate      float64 `json:"tx_rate"`
	Packets     uint64  `json:"packets"`
}

type bucket struct {
	timestamp  time.Time
	hosts      map[string]*hostAccum
	protoBytes map[string]uint64
	ipVerBytes map[string]uint64
}

type hostAccum struct {
	bytes   uint64
	rxBytes uint64 // towards local nets (download)
	txBytes uint64 // from local nets (upload)
	packets uint64
}

type Tracker struct {
	device      string
	promiscuous bool
	localNets   []*net.IPNet // LOCAL_NETS for SPAN port direction detection
	mu          sync.RWMutex
	buckets     []*bucket
	current     *bucket
	stopCh      chan struct{}
	dns         *resolver.Resolver
	geoDB       *geoip.DB
}

func New(device string, promiscuous bool, localNets []*net.IPNet, geoDB *geoip.DB, dns *resolver.Resolver) *Tracker {
	return &Tracker{
		device:      device,
		promiscuous: promiscuous,
		localNets:   localNets,
		buckets:     make([]*bucket, 0, 1440),
		stopCh:      make(chan struct{}),
		dns:         dns,
		geoDB:       geoDB,
	}
}

func (t *Tracker) Run() {
	devices, err := t.getDevices()
	if err != nil {
		fmt.Fprintf(os.Stderr, "talkers: cannot list devices: %v\n", err)
		fmt.Fprintf(os.Stderr, "talkers: top-talkers feature requires root/CAP_NET_RAW\n")
		return
	}
	if len(devices) == 0 {
		fmt.Fprintf(os.Stderr, "talkers: no capture devices found\n")
		return
	}

	t.current = &bucket{
		timestamp:  time.Now().Truncate(bucketSize),
		hosts:      make(map[string]*hostAccum),
		protoBytes: make(map[string]uint64),
		ipVerBytes: make(map[string]uint64),
	}

	go t.rotateBuckets()

	for _, dev := range devices {
		go t.captureDevice(dev)
	}

	<-t.stopCh
}

func (t *Tracker) Stop() {
	close(t.stopCh)
}

func (t *Tracker) TopByVolume(n int) []TalkerStat {
	// Step 1: Copy raw data under lock
	t.mu.RLock()
	totals := make(map[string]*TalkerStat)
	for _, b := range t.buckets {
		for ip, acc := range b.hosts {
			if _, ok := totals[ip]; !ok {
				totals[ip] = &TalkerStat{IP: ip}
			}
			totals[ip].TotalBytes += acc.bytes
			totals[ip].RxBytes += acc.rxBytes
			totals[ip].TxBytes += acc.txBytes
			totals[ip].Packets += acc.packets
		}
	}
	if t.current != nil {
		for ip, acc := range t.current.hosts {
			if _, ok := totals[ip]; !ok {
				totals[ip] = &TalkerStat{IP: ip}
			}
			totals[ip].TotalBytes += acc.bytes
			totals[ip].RxBytes += acc.rxBytes
			totals[ip].TxBytes += acc.txBytes
			totals[ip].Packets += acc.packets
		}
	}
	t.mu.RUnlock()

	// Step 2: Sort + trim before enrichment to avoid unnecessary work
	list := make([]TalkerStat, 0, len(totals))
	for _, s := range totals {
		list = append(list, *s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].TotalBytes > list[j].TotalBytes
	})
	if len(list) > n {
		list = list[:n]
	}

	// Step 3: Enrich outside lock — DNS resolution and GeoIP are expensive
	for i := range list {
		if t.dns != nil {
			list[i].Hostname = t.dns.LookupAddrAsync(list[i].IP)
		}
		t.enrichGeo(&list[i])
	}
	return list
}

func (t *Tracker) TopByBandwidth(n int) []TalkerStat {
	// Step 1: Copy raw data under lock
	t.mu.RLock()
	if t.current == nil {
		t.mu.RUnlock()
		return nil
	}

	elapsed := time.Since(t.current.timestamp).Seconds()
	if elapsed < 1 {
		elapsed = 1
	}

	type rawEntry struct {
		ip      string
		bytes   uint64
		rxBytes uint64
		txBytes uint64
		packets uint64
	}
	raw := make([]rawEntry, 0, len(t.current.hosts))
	for ip, acc := range t.current.hosts {
		raw = append(raw, rawEntry{ip, acc.bytes, acc.rxBytes, acc.txBytes, acc.packets})
	}
	t.mu.RUnlock()

	// Step 2: Build stats, sort, and trim before enrichment
	list := make([]TalkerStat, 0, len(raw))
	for _, r := range raw {
		list = append(list, TalkerStat{
			IP:         r.ip,
			TotalBytes: r.bytes,
			RxBytes:    r.rxBytes,
			TxBytes:    r.txBytes,
			RateBytes:  float64(r.bytes) / elapsed,
			RxRate:     float64(r.rxBytes) / elapsed,
			TxRate:     float64(r.txBytes) / elapsed,
			Packets:    r.packets,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].RateBytes > list[j].RateBytes
	})
	if len(list) > n {
		list = list[:n]
	}

	// Step 3: Enrich outside lock — DNS resolution and GeoIP are expensive
	for i := range list {
		if t.dns != nil {
			list[i].Hostname = t.dns.LookupAddrAsync(list[i].IP)
		}
		t.enrichGeo(&list[i])
	}
	return list
}

func (t *Tracker) getDevices() ([]string, error) {
	if t.device != "" {
		return []string{t.device}, nil
	}

	devs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range devs {
		addrs, _ := d.Addrs()
		if d.Name == "lo" || len(addrs) == 0 {
			continue
		}
		names = append(names, d.Name)
	}
	return names, nil
}

func (t *Tracker) captureDevice(device string) {
	handle, err := packets.FetchPcapSock(device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talkers: cannot open %s: %v\n", device, err)
		return
	}
	defer unix.Close(handle)
	log.Printf("Applying BPF filter %s \n", device)
	if err := packets.ApplyBPFFilter(handle, packets.AnyIpFilter); err != nil {
		fmt.Fprintf(os.Stderr, "talkers: BPF filter error on %s: %v\n", device, err)
	}

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}
		data := make([]byte, snapshotLen)
		numRead, _, err := unix.Recvfrom(handle, data, 0)
		if err != nil {
			// Real error
			fmt.Fprintf(os.Stderr, "talkers: read error on %s: %v\n", device, err)
			return
		}
		t.processPacket(data[:numRead])
	}
}

func (t *Tracker) processPacket(pkt []byte) {
	ipVersion := "IPv4"
	ipPacket := packets.ParseIPPacket(pkt)
	if len(ipPacket.SrcIP) != 4 {
		ipVersion = "IPv6"
	}
	var proto string
	switch ipPacket.Proto {
	case unix.IPPROTO_TCP:
		proto = "TCP"
	case unix.IPPROTO_UDP:
		proto = "UDP"
	case unix.IPPROTO_ICMP, unix.IPPROTO_ICMPV6:
		proto = "ICMP"
	default:
		proto = "Other"
	}

	// Classify IPs outside the lock — avoids holding the write lock
	// while doing net.IP method calls.
	srcLocal := isLocalIP(ipPacket.SrcIP) || t.isLocalNet(ipPacket.SrcIP)
	dstLocal := isLocalIP(ipPacket.DstIP) || t.isLocalNet(ipPacket.DstIP)
	srcStr := ipPacket.SrcIP.String()
	dstStr := ipPacket.DstIP.String()

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return
	}

	for _, entry := range []struct {
		ip    string
		local bool
	}{{srcStr, srcLocal}, {dstStr, dstLocal}} {
		if entry.local {
			continue
		}
		if _, ok := t.current.hosts[entry.ip]; !ok {
			t.current.hosts[entry.ip] = &hostAccum{}
		}
		t.current.hosts[entry.ip].bytes += ipPacket.Len
		t.current.hosts[entry.ip].packets++
	}

	// Direction detection for SPAN/mirror port using LOCAL_NETS
	if len(t.localNets) > 0 {
		if srcLocal && !dstLocal {
			// Local → Remote = upload (TX from local perspective)
			if h, ok := t.current.hosts[dstStr]; ok {
				h.txBytes += ipPacket.Len
			}
		} else if !srcLocal && dstLocal {
			// Remote → Local = download (RX from local perspective)
			if h, ok := t.current.hosts[srcStr]; ok {
				h.rxBytes += ipPacket.Len
			}
		}
	}

	t.current.protoBytes[proto] += ipPacket.Len
	t.current.ipVerBytes[ipVersion] += ipPacket.Len
}

func (t *Tracker) rotateBuckets() {
	ticker := time.NewTicker(bucketSize)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.mu.Lock()
			now := time.Now()
			if t.current != nil {
				t.buckets = append(t.buckets, t.current)
			}
			cutoff := now.Add(-maxAge)
			idx := 0
			for idx < len(t.buckets) && t.buckets[idx].timestamp.Before(cutoff) {
				idx++
			}
			if idx > 0 {
				t.buckets = t.buckets[idx:]
			}
			t.current = &bucket{
				timestamp:  now.Truncate(bucketSize),
				hosts:      make(map[string]*hostAccum),
				protoBytes: make(map[string]uint64),
				ipVerBytes: make(map[string]uint64),
			}
			t.mu.Unlock()
		case <-t.stopCh:
			return
		}
	}
}

// GetProtocolBreakdown returns accumulated bytes per L4 protocol over the 24h window.
func (t *Tracker) GetProtocolBreakdown() map[string]uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	totals := make(map[string]uint64)
	for _, b := range t.buckets {
		for proto, bytes := range b.protoBytes {
			totals[proto] += bytes
		}
	}
	if t.current != nil {
		for proto, bytes := range t.current.protoBytes {
			totals[proto] += bytes
		}
	}
	return totals
}

// GetIPVersionBreakdown returns accumulated bytes per IP version (IPv4/IPv6) over the 24h window.
func (t *Tracker) GetIPVersionBreakdown() map[string]uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	totals := make(map[string]uint64)
	for _, b := range t.buckets {
		for ver, bytes := range b.ipVerBytes {
			totals[ver] += bytes
		}
	}
	if t.current != nil {
		for ver, bytes := range t.current.ipVerBytes {
			totals[ver] += bytes
		}
	}
	return totals
}

// CountryStat holds per-country traffic totals.
type CountryStat struct {
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	Bytes       uint64 `json:"bytes"`
	Connections int    `json:"connections"`
}

// ASNStat holds per-ASN traffic totals.
type ASNStat struct {
	ASN         uint   `json:"asn"`
	ASOrg       string `json:"as_org"`
	Bytes       uint64 `json:"bytes"`
	Connections int    `json:"connections"`
}

// enrichGeo populates geo fields on a TalkerStat from the MMDB.
func (t *Tracker) enrichGeo(s *TalkerStat) {
	if t.geoDB == nil {
		return
	}
	geo := t.geoDB.Lookup(s.IP)
	if geo == nil {
		return
	}
	s.Country = geo.Country
	s.CountryName = geo.CountryName
	s.ASN = geo.ASN
	s.ASOrg = geo.ASOrg
}

// GeoBreakdown holds both per-country and per-ASN traffic summaries,
// computed in a single pass over the IP totals to avoid duplicate work.
type GeoBreakdown struct {
	Countries []CountryStat `json:"countries"`
	ASNs      []ASNStat     `json:"asns"`
}

// GetGeoBreakdown returns traffic grouped by country and by ASN over the
// 24h window.  Both are computed in a single lock + GeoIP pass.
func (t *Tracker) GetGeoBreakdown() *GeoBreakdown {
	if t.geoDB == nil || !t.geoDB.Available() {
		return &GeoBreakdown{}
	}

	// Step 1: Copy raw data under lock
	t.mu.RLock()
	ipTotals := make(map[string]uint64)
	for _, b := range t.buckets {
		for ip, acc := range b.hosts {
			ipTotals[ip] += acc.bytes
		}
	}
	if t.current != nil {
		for ip, acc := range t.current.hosts {
			ipTotals[ip] += acc.bytes
		}
	}
	t.mu.RUnlock()

	// Step 2: Single GeoIP enrichment pass outside lock
	type countryAcc struct {
		name  string
		bytes uint64
		ips   int
	}
	type asnAcc struct {
		org   string
		bytes uint64
		ips   int
	}
	countries := make(map[string]*countryAcc)
	asns := make(map[uint]*asnAcc)

	for ip, bytes := range ipTotals {
		geo := t.geoDB.Lookup(ip)

		// Country aggregation
		cc := "XX"
		cname := "Unknown"
		if geo != nil && geo.Country != "" {
			cc = geo.Country
			cname = geo.CountryName
		}
		if _, ok := countries[cc]; !ok {
			countries[cc] = &countryAcc{name: cname}
		}
		countries[cc].bytes += bytes
		countries[cc].ips++

		// ASN aggregation
		if geo != nil && geo.ASN != 0 {
			if _, ok := asns[geo.ASN]; !ok {
				asns[geo.ASN] = &asnAcc{org: geo.ASOrg}
			}
			asns[geo.ASN].bytes += bytes
			asns[geo.ASN].ips++
		}
	}

	// Build country result
	countryResult := make([]CountryStat, 0, len(countries))
	for cc, acc := range countries {
		countryResult = append(countryResult, CountryStat{
			Country:     cc,
			CountryName: acc.name,
			Bytes:       acc.bytes,
			Connections: acc.ips,
		})
	}
	sort.Slice(countryResult, func(i, j int) bool {
		return countryResult[i].Bytes > countryResult[j].Bytes
	})
	if len(countryResult) > 20 {
		countryResult = countryResult[:20]
	}

	// Build ASN result
	asnResult := make([]ASNStat, 0, len(asns))
	for asn, acc := range asns {
		asnResult = append(asnResult, ASNStat{
			ASN:         asn,
			ASOrg:       acc.org,
			Bytes:       acc.bytes,
			Connections: acc.ips,
		})
	}
	sort.Slice(asnResult, func(i, j int) bool {
		return asnResult[i].Bytes > asnResult[j].Bytes
	})
	if len(asnResult) > 20 {
		asnResult = asnResult[:20]
	}

	return &GeoBreakdown{
		Countries: countryResult,
		ASNs:      asnResult,
	}
}

// isLocalIP checks if an IP is private, loopback, or link-local.
// Uses Go's built-in methods — zero allocations.
func isLocalIP(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

func (t *Tracker) isLocalNet(ip net.IP) bool {
	if len(t.localNets) == 0 {
		return false
	}
	for _, n := range t.localNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
