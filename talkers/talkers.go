package talkers

import (
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"time"

	"bandwidth-monitor/geoip"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcap"
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
	RateBytes   float64 `json:"rate_bytes"`
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
	packets uint64
}

type Tracker struct {
	device      string
	promiscuous bool
	mu          sync.RWMutex
	buckets     []*bucket
	current     *bucket
	stopCh      chan struct{}
	dnsCache    map[string]string
	dnsCacheMu  sync.RWMutex
	geoDB       *geoip.DB
}

func New(device string, promiscuous bool, geoDB *geoip.DB) *Tracker {
	return &Tracker{
		device:      device,
		promiscuous: promiscuous,
		buckets:     make([]*bucket, 0, 1440),
		stopCh:      make(chan struct{}),
		dnsCache:    make(map[string]string),
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
	t.mu.RLock()
	defer t.mu.RUnlock()

	totals := make(map[string]*TalkerStat)
	for _, b := range t.buckets {
		for ip, acc := range b.hosts {
			if _, ok := totals[ip]; !ok {
				totals[ip] = &TalkerStat{IP: ip}
			}
			totals[ip].TotalBytes += acc.bytes
			totals[ip].Packets += acc.packets
		}
	}
	if t.current != nil {
		for ip, acc := range t.current.hosts {
			if _, ok := totals[ip]; !ok {
				totals[ip] = &TalkerStat{IP: ip}
			}
			totals[ip].TotalBytes += acc.bytes
			totals[ip].Packets += acc.packets
		}
	}

	list := make([]TalkerStat, 0, len(totals))
	for _, s := range totals {
		s.Hostname = t.resolveIP(s.IP)
		t.enrichGeo(s)
		list = append(list, *s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].TotalBytes > list[j].TotalBytes
	})
	if len(list) > n {
		list = list[:n]
	}
	return list
}

func (t *Tracker) TopByBandwidth(n int) []TalkerStat {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.current == nil {
		return nil
	}

	elapsed := time.Since(t.current.timestamp).Seconds()
	if elapsed < 1 {
		elapsed = 1
	}

	list := make([]TalkerStat, 0, len(t.current.hosts))
	for ip, acc := range t.current.hosts {
		s := TalkerStat{
			IP:         ip,
			Hostname:   t.resolveIP(ip),
			TotalBytes: acc.bytes,
			RateBytes:  float64(acc.bytes) / elapsed,
			Packets:    acc.packets,
		}
		t.enrichGeo(&s)
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].RateBytes > list[j].RateBytes
	})
	if len(list) > n {
		list = list[:n]
	}
	return list
}

func (t *Tracker) getDevices() ([]string, error) {
	if t.device != "" {
		return []string{t.device}, nil
	}
	devs, err := pcap.FindAllDevs()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range devs {
		if d.Name == "lo" || len(d.Addresses) == 0 {
			continue
		}
		names = append(names, d.Name)
	}
	return names, nil
}

func (t *Tracker) captureDevice(device string) {
	handle, err := pcap.OpenLive(device, snapshotLen, t.promiscuous, capTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "talkers: cannot open %s: %v\n", device, err)
		return
	}
	defer handle.Close()

	if err := handle.SetBPFFilter("ip or ip6"); err != nil {
		fmt.Fprintf(os.Stderr, "talkers: BPF filter error on %s: %v\n", device, err)
	}

	for {
		select {
		case <-t.stopCh:
			return
		default:
		}
		data, ci, err := handle.ReadPacketData()
		if err != nil {
			// Timeout is expected — just loop
			if err == pcap.NextErrorTimeoutExpired {
				continue
			}
			// Real error
			fmt.Fprintf(os.Stderr, "talkers: read error on %s: %v\n", device, err)
			return
		}
		pkt := gopacket.NewPacket(data, handle.LinkType(), gopacket.DecodeOptions{
			Lazy:   true,
			NoCopy: true,
		})
		_ = ci
		t.processPacket(pkt)
	}
}

func (t *Tracker) processPacket(pkt gopacket.Packet) {
	var srcIP, dstIP string
	var pktLen uint64
	var ipVersion string

	if ipLayer := pkt.Layer(layers.LayerTypeIPv4); ipLayer != nil {
		ip := ipLayer.(*layers.IPv4)
		srcIP = ip.SrcIP.String()
		dstIP = ip.DstIP.String()
		pktLen = uint64(ip.Length)
		ipVersion = "IPv4"
	} else if ipLayer := pkt.Layer(layers.LayerTypeIPv6); ipLayer != nil {
		ip := ipLayer.(*layers.IPv6)
		srcIP = ip.SrcIP.String()
		dstIP = ip.DstIP.String()
		pktLen = uint64(ip.Length) + 40
		ipVersion = "IPv6"
	} else {
		return
	}

	var proto string
	if pkt.Layer(layers.LayerTypeTCP) != nil {
		proto = "TCP"
	} else if pkt.Layer(layers.LayerTypeUDP) != nil {
		proto = "UDP"
	} else if pkt.Layer(layers.LayerTypeICMPv4) != nil || pkt.Layer(layers.LayerTypeICMPv6) != nil {
		proto = "ICMP"
	} else {
		proto = "Other"
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.current == nil {
		return
	}

	for _, ip := range []string{srcIP, dstIP} {
		if isPrivateIP(ip) {
			continue
		}
		if _, ok := t.current.hosts[ip]; !ok {
			t.current.hosts[ip] = &hostAccum{}
		}
		t.current.hosts[ip].bytes += pktLen
		t.current.hosts[ip].packets++
	}

	t.current.protoBytes[proto] += pktLen
	t.current.ipVerBytes[ipVersion] += pktLen
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

func (t *Tracker) resolveIP(ip string) string {
	t.dnsCacheMu.RLock()
	name, ok := t.dnsCache[ip]
	t.dnsCacheMu.RUnlock()

	if ok {
		return name
	}

	// Store IP as placeholder immediately so we don't re-trigger
	t.dnsCacheMu.Lock()
	// Double-check after acquiring write lock
	if name, ok := t.dnsCache[ip]; ok {
		t.dnsCacheMu.Unlock()
		return name
	}
	t.dnsCache[ip] = ip
	t.dnsCacheMu.Unlock()

	// Resolve asynchronously
	go func() {
		names, err := net.LookupAddr(ip)
		if err != nil || len(names) == 0 {
			return
		}
		name := names[0]
		if len(name) > 0 && name[len(name)-1] == '.' {
			name = name[:len(name)-1]
		}
		t.dnsCacheMu.Lock()
		t.dnsCache[ip] = name
		t.dnsCacheMu.Unlock()
	}()

	return ip
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

// GetCountryBreakdown returns traffic grouped by country over the 24h window.
func (t *Tracker) GetCountryBreakdown() []CountryStat {
	if t.geoDB == nil || !t.geoDB.Available() {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	// Aggregate bytes per IP across all buckets
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

	// Group by country
	type countryAcc struct {
		name  string
		bytes uint64
		ips   int
	}
	countries := make(map[string]*countryAcc)
	for ip, bytes := range ipTotals {
		geo := t.geoDB.Lookup(ip)
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
	}

	result := make([]CountryStat, 0, len(countries))
	for cc, acc := range countries {
		result = append(result, CountryStat{
			Country:     cc,
			CountryName: acc.name,
			Bytes:       acc.bytes,
			Connections: acc.ips,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Bytes > result[j].Bytes
	})
	if len(result) > 20 {
		result = result[:20]
	}
	return result
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

// ASNStat holds per-ASN traffic totals.
type ASNStat struct {
	ASN         uint   `json:"asn"`
	ASOrg       string `json:"as_org"`
	Bytes       uint64 `json:"bytes"`
	Connections int    `json:"connections"`
}

// GetASNBreakdown returns traffic grouped by autonomous system over the 24h window.
func (t *Tracker) GetASNBreakdown() []ASNStat {
	if t.geoDB == nil || !t.geoDB.Available() {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

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

	type asnAcc struct {
		org   string
		bytes uint64
		ips   int
	}
	asns := make(map[uint]*asnAcc)
	for ip, bytes := range ipTotals {
		geo := t.geoDB.Lookup(ip)
		var asn uint
		var org string
		if geo != nil && geo.ASN != 0 {
			asn = geo.ASN
			org = geo.ASOrg
		}
		if asn == 0 {
			continue
		}
		if _, ok := asns[asn]; !ok {
			asns[asn] = &asnAcc{org: org}
		}
		asns[asn].bytes += bytes
		asns[asn].ips++
	}

	result := make([]ASNStat, 0, len(asns))
	for asn, acc := range asns {
		result = append(result, ASNStat{
			ASN:         asn,
			ASOrg:       acc.org,
			Bytes:       acc.bytes,
			Connections: acc.ips,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Bytes > result[j].Bytes
	})
	if len(result) > 20 {
		result = result[:20]
	}
	return result
}

func isPrivateIP(ipStr string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	privateRanges := []struct {
		network *net.IPNet
	}{
		{parseCIDR("10.0.0.0/8")},
		{parseCIDR("172.16.0.0/12")},
		{parseCIDR("192.168.0.0/16")},
		{parseCIDR("127.0.0.0/8")},
		{parseCIDR("fc00::/7")},
		{parseCIDR("::1/128")},
		{parseCIDR("fe80::/10")},
	}
	for _, r := range privateRanges {
		if r.network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}
