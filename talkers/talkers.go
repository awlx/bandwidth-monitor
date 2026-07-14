package talkers

import (
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"bandwidth-monitor/geoip"
	"bandwidth-monitor/netutil"
	"bandwidth-monitor/packets"
	"bandwidth-monitor/resolver"

	"golang.org/x/sys/unix"
)

const (
	snapshotLen       int32         = 128
	capTimeout        time.Duration = 100 * time.Millisecond
	bucketSize                      = 1 * time.Minute
	maxAge                          = 24 * time.Hour
	maxHostsPerBucket               = 10000 // cap to bound memory on busy routers

	// Pair tracking (local host -> remote host) for ASN/provider drill-downs.
	maxPairsPerBucket      = 20000 // total distinct (local,remote) pairs per bucket
	maxRemotesPerLocalHost = 500   // distinct remotes tracked per local host per bucket

	// Rate ring: short circular buffer for responsive rate calculation.
	// 6 slots × 5s = 30s window. Rates are computed over the filled
	// portion of the ring, so peaks show within 5–10s instead of 60–120s.
	rateSlotDuration = 5 * time.Second
	rateSlotCount    = 6

	// Direct captures use independent one-second slots so the CLI can report
	// iftop-style 2s, 10s, and 40s windows without changing daemon rates.
	directRateSlotDuration = time.Second
	directRateSlotCount    = 41
	maxDirectFlowsPerSlot  = 4096
)

type TalkerKey struct {
	IP string `json:"ip"`
}

type TalkerStat struct {
	IP          string  `json:"ip"`
	Hostname    string  `json:"hostname"`
	Country     string  `json:"country,omitempty"`
	CountryName string  `json:"country_name,omitempty"`
	City        string  `json:"city,omitempty"`
	Latitude    float64 `json:"lat,omitempty"`
	Longitude   float64 `json:"lon,omitempty"`
	ASN         uint    `json:"asn,omitempty"`
	ASOrg       string  `json:"as_org,omitempty"`
	TotalBytes  uint64  `json:"total_bytes"`
	RxBytes     uint64  `json:"rx_bytes"`
	TxBytes     uint64  `json:"tx_bytes"`
	RateBytes   float64 `json:"rate_bytes"`
	RxRate      float64 `json:"rx_rate"`
	TxRate      float64 `json:"tx_rate"`
	Packets     uint64  `json:"packets"`
	IsLocal     bool    `json:"is_local,omitempty"`
}

type talkerScope uint8

const (
	talkerScopeAll talkerScope = iota
	talkerScopeRemote
	talkerScopeClient
)

// DirectTalkerStat is a remote peer observed by a direct single-interface
// capture, with rates expressed relative to the packet's actual local endpoint.
type DirectTalkerStat struct {
	LocalIP string
	TalkerStat
	Protocol   string
	RemotePort uint16
	HasPort    bool
	RxRate10   float64
	RxRate40   float64
	TxRate10   float64
	TxRate40   float64
}

// DirectRateTotals contains all observed direct peers, including rows below
// the display limit.
type DirectRateTotals struct {
	RxRate   float64
	RxRate10 float64
	RxRate40 float64
	TxRate   float64
	TxRate10 float64
	TxRate40 float64
	RxBytes  uint64
	TxBytes  uint64
}

type directPairKey struct {
	local  string
	remote string
}

type directFlowKey struct {
	directPairKey
	protocol   string
	remotePort uint16
	hasPort    bool
}

// DirectViewMode selects a concurrently maintained direct-capture aggregation.
type DirectViewMode uint8

const (
	DirectViewHosts DirectViewMode = iota
	DirectViewPorts
)

type bucket struct {
	timestamp  time.Time
	hosts      map[string]*hostAccum
	protoBytes map[string]uint64
	ipVerBytes map[string]uint64

	// pairs tracks per-(local host, remote host) byte/packet totals, keyed
	// by local IP -> remote IP. This powers "which of my machines talk to
	// ASN/provider X" drill-downs, which need local-host attribution that
	// the flat `hosts` totals above cannot provide (hosts only tracks each
	// IP's own totals, not who it talked to).
	pairs map[string]map[string]*hostAccum
}

// rateSlot is one slot in the short rate ring buffer.
type rateSlot struct {
	timestamp time.Time
	hosts     map[string]*hostAccum
}

type directRateSlot struct {
	timestamp time.Time
	peers     map[directPairKey]*hostAccum
	flows     map[directFlowKey]*hostAccum
}

type hostAccum struct {
	bytes   uint64
	rxBytes uint64 // towards local nets (download)
	txBytes uint64 // from local nets (upload)
	packets uint64
}

// parsedPkt holds the pre-parsed fields of a single packet for batch processing.
type parsedPkt struct {
	srcStr, dstStr       string
	srcLocal, dstLocal   bool
	srcSelf, dstSelf     bool
	srcLoopLL, dstLoopLL bool
	wireLen              uint64
	proto                string
	ipVersion            string
	transportProtocol    string
	srcPort, dstPort     uint16
	hasPorts             bool
}

type Tracker struct {
	devices     []string
	promiscuous bool
	localNets   []*net.IPNet        // LOCAL_NETS for direction detection
	selfIPs     map[string]struct{} // router's own interface IPs for direction tiebreaker
	lanDevices  map[string]bool     // LAN-facing interfaces (have private addrs) — only these count hosts
	mu          sync.RWMutex
	buckets     []*bucket
	current     *bucket
	stopCh      chan struct{}
	doneCh      chan struct{}
	dns         *resolver.Resolver
	geoDB       *geoip.DB
	lifecycleMu sync.Mutex
	workers     sync.WaitGroup
	started     bool
	stopped     bool
	direct      bool
	errCh       chan error
	captureFn   func(string) error

	// Rate ring: short circular buffer (5s slots) for responsive rate calc.
	// Protected by the same mu as buckets/current.
	rateRing    [rateSlotCount]*rateSlot
	rateRingIdx int // index of current slot in rateRing

	directRateRing    [directRateSlotCount]*directRateSlot
	directRateRingIdx int
	directRxBytes     uint64
	directTxBytes     uint64
}

func New(devices []string, promiscuous bool, localNets []*net.IPNet, geoDB *geoip.DB, dns *resolver.Resolver) *Tracker {
	return newTracker(devices, promiscuous, localNets, geoDB, dns, false)
}

// NewDirect creates a tracker that captures exactly one explicitly selected
// interface, regardless of whether it is LAN-facing. It is intended for
// ad-hoc tools; the server's New constructor retains LAN-only accounting.
func NewDirect(device string, promiscuous bool, localNets []*net.IPNet, geoDB *geoip.DB, dns *resolver.Resolver) *Tracker {
	return newTracker([]string{device}, promiscuous, localNets, geoDB, dns, true)
}

// isLANAddress reports whether ip marks its interface as LAN-facing:
// inside a configured local network, or private when none are configured.
// Link-local addresses never count — every interface has one.
func isLANAddress(ip net.IP, localNets []*net.IPNet) bool {
	if ip == nil || ip.IsLinkLocalUnicast() {
		return false
	}
	return netutil.IsLocal(ip, localNets)
}

func newTracker(devices []string, promiscuous bool, localNets []*net.IPNet, geoDB *geoip.DB, dns *resolver.Resolver, direct bool) *Tracker {
	// Build a set of the router's own interface IPs so we can resolve
	// direction when both endpoints fall within localNets (e.g. the
	// router's WAN IP talking to a remote host through a tunnel).
	selfIPs := make(map[string]struct{})
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				selfIPs[ipnet.IP.String()] = struct{}{}
			}
		}
	}
	if len(selfIPs) > 0 {
		log.Printf("talkers: %d self IPs for direction detection", len(selfIPs))
	}

	// Identify LAN-facing interfaces: L2 (Ethernet) interfaces that have
	// at least one address inside localNets (LOCAL_NETS), or — when no
	// local networks are configured — a private (RFC 1918 / ULA) address.
	// Publicly-addressed LANs (PI space) are only recognised via
	// LOCAL_NETS. Only LAN interfaces count per-host traffic to avoid
	// double-counting packets that traverse multiple interfaces (e.g.
	// WAN → kernel routing → LAN, or tunnel → kernel → LAN).
	//
	// L3 interfaces (WireGuard, PPP, tun) are excluded even if they have
	// private tunnel IPs (e.g. 10.x.x.x) — they are tunnel/WAN endpoints,
	// not LAN segments. Counting on the LAN side gives a single, consistent
	// view of who is talking to whom.
	lanDevices := make(map[string]bool)
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if iface.Name == "lo" {
				continue
			}
			// Skip L3 interfaces — tunnels and WAN, never LAN
			if packets.IsL3Device(iface.Name) {
				continue
			}
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				ipnet, ok := addr.(*net.IPNet)
				if !ok {
					continue
				}
				if isLANAddress(ipnet.IP, localNets) {
					lanDevices[iface.Name] = true
					break
				}
			}
		}
	}
	if len(lanDevices) > 0 {
		names := make([]string, 0, len(lanDevices))
		for name := range lanDevices {
			names = append(names, name)
		}
		log.Printf("talkers: LAN interfaces for host accounting: %s", strings.Join(names, ", "))
	}

	trk := &Tracker{
		devices:     devices,
		promiscuous: promiscuous,
		localNets:   localNets,
		selfIPs:     selfIPs,
		lanDevices:  lanDevices,
		buckets:     make([]*bucket, 0, 1440),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		dns:         dns,
		geoDB:       geoDB,
		direct:      direct,
		errCh:       make(chan error, 1),
	}
	trk.captureFn = trk.captureDevice
	// Initialize first rate ring slot
	trk.rateRing[0] = &rateSlot{
		timestamp: time.Now(),
		hosts:     make(map[string]*hostAccum),
	}
	if direct {
		trk.directRateRing[0] = &directRateSlot{
			timestamp: time.Now(),
			peers:     make(map[directPairKey]*hostAccum),
			flows:     make(map[directFlowKey]*hostAccum),
		}
	}
	return trk
}

func (t *Tracker) Run() {
	t.lifecycleMu.Lock()
	if t.started {
		t.lifecycleMu.Unlock()
		panic("talkers: Tracker.Run called more than once")
	}
	if t.stopped {
		t.lifecycleMu.Unlock()
		return
	}
	t.started = true
	t.lifecycleMu.Unlock()
	defer close(t.doneCh)

	devices, err := t.getDevices()
	if err != nil {
		log.Printf("talkers: cannot list devices: %v", err)
		log.Println("talkers: top-talkers feature requires root/CAP_NET_RAW")
		return
	}
	if len(devices) == 0 {
		log.Println("talkers: no capture devices found")
		return
	}

	t.mu.Lock()
	t.current = &bucket{
		timestamp:  time.Now().Truncate(bucketSize),
		hosts:      make(map[string]*hostAccum),
		protoBytes: make(map[string]uint64),
		ipVerBytes: make(map[string]uint64),
		pairs:      make(map[string]map[string]*hostAccum),
	}
	t.mu.Unlock()

	t.lifecycleMu.Lock()
	if t.stopped {
		t.lifecycleMu.Unlock()
		return
	}
	t.startWorker(t.rotateBuckets)
	t.startWorker(t.rotateRateRing)
	if t.direct {
		t.startWorker(t.rotateDirectRateRing)
	}
	for _, dev := range devices {
		if !t.direct && !t.lanDevices[dev] {
			log.Printf("talkers: skipping capture on %s (not LAN)", dev)
			continue
		}
		device := dev
		t.startWorker(func() {
			if err := t.captureFn(device); err != nil {
				log.Printf("talkers: capture on %s failed: %v", device, err)
				select {
				case t.errCh <- err:
				default:
				}
			}
		})
	}

	t.lifecycleMu.Unlock()

	<-t.stopCh
	t.workers.Wait()
}

// Errors reports capture failures without making table refreshes block.
func (t *Tracker) Errors() <-chan error { return t.errCh }

// Stop signals all tracker goroutines and waits for them to release their resources.
// It is safe to call multiple times or concurrently.
func (t *Tracker) Stop() {
	t.lifecycleMu.Lock()
	if !t.stopped {
		close(t.stopCh)
	}
	t.stopped = true
	var doneCh <-chan struct{}
	if t.started {
		doneCh = t.doneCh
	}
	t.lifecycleMu.Unlock()

	if doneCh != nil {
		<-doneCh
	}
}

func (t *Tracker) startWorker(fn func()) {
	t.workers.Add(1)
	go func() {
		defer t.workers.Done()
		fn()
	}()
}

func (t *Tracker) TopByVolume(n int) []TalkerStat {
	return t.topByVolume(n, talkerScopeAll)
}

// TopRemoteByVolume returns the highest-volume non-local hosts without local
// clients consuming slots in the requested limit.
func (t *Tracker) TopRemoteByVolume(n int) []TalkerStat {
	return t.topByVolume(n, talkerScopeRemote)
}

// TopClientsByVolume returns the highest-volume local clients over the rolling
// 24-hour window, excluding the router's own addresses.
func (t *Tracker) TopClientsByVolume(n int) []TalkerStat {
	return t.topByVolume(n, talkerScopeClient)
}

func (t *Tracker) topByVolume(n int, scope talkerScope) []TalkerStat {
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
		isLocal, include := t.talkerInScope(s.IP, scope)
		if !include {
			continue
		}
		s.IsLocal = isLocal
		list = append(list, *s)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].TotalBytes == list[j].TotalBytes {
			return list[i].IP < list[j].IP
		}
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
		t.geoDB.Enrich(list[i].IP, &list[i])
	}
	return list
}

func (t *Tracker) talkerInScope(ipStr string, scope talkerScope) (bool, bool) {
	if _, isSelf := t.selfIPs[ipStr]; isSelf {
		return false, false
	}
	ip := net.ParseIP(ipStr)
	if ip != nil && ip.IsLoopback() {
		return false, false
	}
	isLocal := ip != nil && netutil.IsLocal(ip, t.localNets)
	switch scope {
	case talkerScopeRemote:
		return false, !isLocal
	case talkerScopeClient:
		return true, isLocal
	default:
		return isLocal, true
	}
}

// TopByCountry returns the top n IPs (by 24h total bytes) whose GeoIP country
// code matches cc. The global Top Talkers lists (TopByVolume/TopByBandwidth)
// only ever cover the overall top-n IPs, which frequently contain nothing for
// a given country — this powers the "click a country on the world map" UI
// drill-down so it can find IPs regardless of their rank in the global list.
func (t *Tracker) TopByCountry(cc string, n int) []TalkerStat {
	if t.geoDB == nil || !t.geoDB.Available() || cc == "" {
		return nil
	}

	// Step 1: Copy raw per-IP totals under lock (same source as TopByVolume).
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

	// Step 2: GeoIP lookup outside the lock to find IPs matching cc. Every
	// known IP has to be checked (same cost as GetGeoBreakdown) since the
	// country isn't known ahead of a lookup; this endpoint is only called
	// on-demand (user click), not on every SSE tick.
	list := make([]TalkerStat, 0, 16)
	for _, s := range totals {
		ip := net.ParseIP(s.IP)
		if _, isSelf := t.selfIPs[s.IP]; isSelf {
			continue
		}
		if ip != nil && ip.IsLoopback() {
			continue
		}
		if ip != nil && netutil.IsLocal(ip, t.localNets) {
			continue
		}
		geo := t.geoDB.Lookup(s.IP)
		if geo == nil || geo.Country != cc {
			continue
		}
		s.SetGeo(geo.Country, geo.CountryName, geo.City, geo.Latitude, geo.Longitude, geo.ASN, geo.ASOrg)
		list = append(list, *s)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].TotalBytes > list[j].TotalBytes
	})
	if len(list) > n {
		list = list[:n]
	}

	// Step 3: Hostname (DNS) enrichment only for the trimmed result.
	for i := range list {
		if t.dns != nil {
			list[i].Hostname = t.dns.LookupAddrAsync(list[i].IP)
		}
	}
	return list
}

func (t *Tracker) TopByBandwidth(n int) []TalkerStat {
	return t.topByBandwidth(n, talkerScopeAll)
}

// TopRemoteByBandwidth returns the busiest non-local hosts without local
// clients consuming slots in the requested limit.
func (t *Tracker) TopRemoteByBandwidth(n int) []TalkerStat {
	return t.topByBandwidth(n, talkerScopeRemote)
}

// TopClientsByBandwidth returns the busiest local clients by current rate,
// excluding the router's own addresses.
func (t *Tracker) TopClientsByBandwidth(n int) []TalkerStat {
	return t.topByBandwidth(n, talkerScopeClient)
}

func (t *Tracker) topByBandwidth(n int, scope talkerScope) []TalkerStat {
	// Use the short rate ring (5s slots, ~30s window) for responsive rate
	// calculation. The 1-minute buckets are still used for 24h volume.
	t.mu.RLock()
	if t.current == nil {
		t.mu.RUnlock()
		return nil
	}

	rates, elapsed := t.rateFromRing()
	t.mu.RUnlock()

	// Step 2: Build stats, sort, and trim before enrichment
	list := make([]TalkerStat, 0, len(rates))
	for ip, r := range rates {
		isLocal, include := t.talkerInScope(ip, scope)
		if !include {
			continue
		}
		list = append(list, TalkerStat{
			IP:         ip,
			TotalBytes: r.bytes,
			RxBytes:    r.rxBytes,
			TxBytes:    r.txBytes,
			RateBytes:  float64(r.bytes) / elapsed,
			RxRate:     float64(r.rxBytes) / elapsed,
			TxRate:     float64(r.txBytes) / elapsed,
			Packets:    r.packets,
			IsLocal:    isLocal,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].RateBytes == list[j].RateBytes {
			return list[i].IP < list[j].IP
		}
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
		t.geoDB.Enrich(list[i].IP, &list[i])
	}
	return list
}

// DirectTopByBandwidth returns one row per local/remote pair for a direct
// capture. Packets with two local or two remote endpoints are intentionally
// excluded because they do not define an unambiguous local-to-remote flow.
// The daemon's endpoint-based TopByBandwidth accounting is unchanged.
func (t *Tracker) DirectTopByBandwidth(n int) []DirectTalkerStat {
	rows, _ := t.DirectBandwidthSnapshot(n)
	return rows
}

// DirectBandwidthSnapshot returns ranked peers and all-peer rolling totals.
func (t *Tracker) DirectBandwidthSnapshot(n int) ([]DirectTalkerStat, DirectRateTotals) {
	return t.DirectBandwidthSnapshotForMode(DirectViewHosts, n)
}

// DirectBandwidthSnapshotForMode selects host or port aggregation without
// changing capture state or resetting either rolling index.
func (t *Tracker) DirectBandwidthSnapshotForMode(mode DirectViewMode, n int) ([]DirectTalkerStat, DirectRateTotals) {
	if mode == DirectViewPorts {
		return t.directPortBandwidthSnapshot(n, time.Now())
	}
	return t.directBandwidthSnapshot(n, time.Now())
}

func (t *Tracker) directBandwidthSnapshot(n int, now time.Time) ([]DirectTalkerStat, DirectRateTotals) {
	if !t.direct || n <= 0 {
		return nil, DirectRateTotals{}
	}

	t.mu.RLock()
	if t.current == nil {
		t.mu.RUnlock()
		return nil, DirectRateTotals{}
	}
	rates2, elapsed2 := t.directRateFromRing(now, 2*time.Second)
	rates10, elapsed10 := t.directRateFromRing(now, 10*time.Second)
	rates40, elapsed40 := t.directRateFromRing(now, 40*time.Second)
	rxBytes, txBytes := t.directRxBytes, t.directTxBytes
	t.mu.RUnlock()

	list := make([]DirectTalkerStat, 0, len(rates40))
	var totals DirectRateTotals
	for pair, rate40 := range rates40 {
		rate2 := rates2[pair]
		rate10 := rates10[pair]
		if rate2 == nil {
			rate2 = &hostAccum{}
		}
		if rate10 == nil {
			rate10 = &hostAccum{}
		}
		stat := DirectTalkerStat{
			LocalIP: pair.local,
			TalkerStat: TalkerStat{
				IP:         pair.remote,
				TotalBytes: rate40.bytes,
				RxBytes:    rate40.rxBytes,
				TxBytes:    rate40.txBytes,
				RateBytes:  float64(rate2.bytes) / elapsed2,
				RxRate:     float64(rate2.rxBytes) / elapsed2,
				TxRate:     float64(rate2.txBytes) / elapsed2,
				Packets:    rate40.packets,
			},
			RxRate10: float64(rate10.rxBytes) / elapsed10,
			RxRate40: float64(rate40.rxBytes) / elapsed40,
			TxRate10: float64(rate10.txBytes) / elapsed10,
			TxRate40: float64(rate40.txBytes) / elapsed40,
		}
		totals.RxRate += stat.RxRate
		totals.RxRate10 += stat.RxRate10
		totals.RxRate40 += stat.RxRate40
		totals.TxRate += stat.TxRate
		totals.TxRate10 += stat.TxRate10
		totals.TxRate40 += stat.TxRate40
		list = append(list, stat)
	}
	totals.RxBytes = rxBytes
	totals.TxBytes = txBytes
	sort.Slice(list, func(i, j int) bool {
		if list[i].RateBytes != list[j].RateBytes {
			return list[i].RateBytes > list[j].RateBytes
		}
		if list[i].IP != list[j].IP {
			return list[i].IP < list[j].IP
		}
		return list[i].LocalIP < list[j].LocalIP
	})
	if len(list) > n {
		list = list[:n]
	}
	for i := range list {
		if t.dns != nil {
			list[i].Hostname = t.dns.LookupAddrAsync(list[i].IP)
		}
		t.geoDB.Enrich(list[i].IP, &list[i].TalkerStat)
	}
	return list, totals
}

func (t *Tracker) directPortBandwidthSnapshot(n int, now time.Time) ([]DirectTalkerStat, DirectRateTotals) {
	if !t.direct || n <= 0 {
		return nil, DirectRateTotals{}
	}

	t.mu.RLock()
	if t.current == nil {
		t.mu.RUnlock()
		return nil, DirectRateTotals{}
	}
	rates2, elapsed2 := t.directFlowRateFromRing(now, 2*time.Second)
	rates10, elapsed10 := t.directFlowRateFromRing(now, 10*time.Second)
	rates40, elapsed40 := t.directFlowRateFromRing(now, 40*time.Second)
	peerRates2, peerElapsed2 := t.directRateFromRing(now, 2*time.Second)
	peerRates10, peerElapsed10 := t.directRateFromRing(now, 10*time.Second)
	peerRates40, peerElapsed40 := t.directRateFromRing(now, 40*time.Second)
	rxBytes, txBytes := t.directRxBytes, t.directTxBytes
	t.mu.RUnlock()

	list := make([]DirectTalkerStat, 0, len(rates40))
	for flow, rate40 := range rates40 {
		rate2 := rates2[flow]
		rate10 := rates10[flow]
		if rate2 == nil {
			rate2 = &hostAccum{}
		}
		if rate10 == nil {
			rate10 = &hostAccum{}
		}
		list = append(list, DirectTalkerStat{
			LocalIP: flow.local,
			TalkerStat: TalkerStat{
				IP: flow.remote, TotalBytes: rate40.bytes,
				RxBytes: rate40.rxBytes, TxBytes: rate40.txBytes,
				RateBytes: float64(rate2.bytes) / elapsed2,
				RxRate:    float64(rate2.rxBytes) / elapsed2,
				TxRate:    float64(rate2.txBytes) / elapsed2,
				Packets:   rate40.packets,
			},
			Protocol: flow.protocol, RemotePort: flow.remotePort, HasPort: flow.hasPort,
			RxRate10: float64(rate10.rxBytes) / elapsed10,
			RxRate40: float64(rate40.rxBytes) / elapsed40,
			TxRate10: float64(rate10.txBytes) / elapsed10,
			TxRate40: float64(rate40.txBytes) / elapsed40,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].RateBytes != list[j].RateBytes {
			return list[i].RateBytes > list[j].RateBytes
		}
		if list[i].IP != list[j].IP {
			return list[i].IP < list[j].IP
		}
		if list[i].LocalIP != list[j].LocalIP {
			return list[i].LocalIP < list[j].LocalIP
		}
		if list[i].Protocol != list[j].Protocol {
			return list[i].Protocol < list[j].Protocol
		}
		if list[i].HasPort != list[j].HasPort {
			return list[i].HasPort
		}
		return list[i].RemotePort < list[j].RemotePort
	})
	if len(list) > n {
		list = list[:n]
	}
	for i := range list {
		if t.dns != nil {
			list[i].Hostname = t.dns.LookupAddrAsync(list[i].IP)
		}
		t.geoDB.Enrich(list[i].IP, &list[i].TalkerStat)
	}
	totals := directRateTotals(
		peerRates2, peerElapsed2, peerRates10, peerElapsed10,
		peerRates40, peerElapsed40,
	)
	totals.RxBytes = rxBytes
	totals.TxBytes = txBytes
	return list, totals
}

func directRateTotals(
	rates2 map[directPairKey]*hostAccum, elapsed2 float64,
	rates10 map[directPairKey]*hostAccum, elapsed10 float64,
	rates40 map[directPairKey]*hostAccum, elapsed40 float64,
) DirectRateTotals {
	var totals DirectRateTotals
	for _, rate := range rates2 {
		totals.RxRate += float64(rate.rxBytes) / elapsed2
		totals.TxRate += float64(rate.txBytes) / elapsed2
	}
	for _, rate := range rates10 {
		totals.RxRate10 += float64(rate.rxBytes) / elapsed10
		totals.TxRate10 += float64(rate.txBytes) / elapsed10
	}
	for _, rate := range rates40 {
		totals.RxRate40 += float64(rate.rxBytes) / elapsed40
		totals.TxRate40 += float64(rate.txBytes) / elapsed40
	}
	return totals
}

// BandwidthForIPs returns current bandwidth stats for the given IP list.
//
// It uses the short rate ring (same source as TopByBandwidth) but only for
// explicitly requested IPs, avoiding top-N truncation and expensive enrichment.
func (t *Tracker) BandwidthForIPs(ips []string) []TalkerStat {
	if len(ips) == 0 {
		return nil
	}

	wanted := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		if ip == "" {
			continue
		}
		wanted[ip] = struct{}{}
	}
	if len(wanted) == 0 {
		return nil
	}

	t.mu.RLock()
	if t.current == nil {
		t.mu.RUnlock()
		return nil
	}
	rates, elapsed := t.rateFromRing()
	t.mu.RUnlock()

	list := make([]TalkerStat, 0, len(wanted))
	for ip := range wanted {
		r, ok := rates[ip]
		if !ok {
			continue
		}
		parsedIP := net.ParseIP(ip)
		if _, isSelf := t.selfIPs[ip]; isSelf {
			continue
		}
		if parsedIP != nil && parsedIP.IsLoopback() {
			continue
		}
		isLocal := parsedIP != nil && netutil.IsLocal(parsedIP, t.localNets)
		list = append(list, TalkerStat{
			IP:         ip,
			TotalBytes: r.bytes,
			RxBytes:    r.rxBytes,
			TxBytes:    r.txBytes,
			RateBytes:  float64(r.bytes) / elapsed,
			RxRate:     float64(r.rxBytes) / elapsed,
			TxRate:     float64(r.txBytes) / elapsed,
			Packets:    r.packets,
			IsLocal:    isLocal,
		})
	}

	sort.Slice(list, func(i, j int) bool {
		return list[i].RateBytes > list[j].RateBytes
	})

	return list
}

func (t *Tracker) getDevices() ([]string, error) {
	if len(t.devices) > 0 {
		return t.devices, nil
	}

	devs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var names []string
	for _, d := range devs {
		addrs, err := d.Addrs()
		if err != nil {
			continue
		}
		if d.Name == "lo" || len(addrs) == 0 {
			continue
		}
		names = append(names, d.Name)
	}
	return names, nil
}

func (t *Tracker) captureDevice(device string) error {
	ring, err := packets.NewRing(device, t.promiscuous)
	if err != nil {
		return err
	}
	defer ring.Close()
	log.Printf("talkers: packet capture active on %s", device)

	// IP string cache: avoids heap-allocating net.IP.String() for every
	// packet. At 10 MB/s there are ~7000 pps but only 10-100 unique IPs.
	// The cache hits 99%+ of lookups, eliminating the main GC bottleneck.
	ipStrCache := make(map[[16]byte]string, 256)
	ipCacheResets := 0
	ipStr := func(ip net.IP) string {
		var k [16]byte
		copy(k[:], ip.To16())
		if s, ok := ipStrCache[k]; ok {
			return s
		}
		s := ip.String()
		ipStrCache[k] = s
		return s
	}

	// Pre-allocate batch buffer (reused across blocks)
	batch := make([]parsedPkt, 0, 256)

	for {
		select {
		case <-t.stopCh:
			return nil
		default:
		}

		// Periodically reset the IP string cache to bound memory on
		// routers that see a large number of unique IPs over time.
		if len(ipStrCache) > 100000 {
			ipStrCache = make(map[[16]byte]string, 256)
			ipCacheResets++
			if ipCacheResets == 1 {
				log.Printf("talkers: reset IP string cache on %s (>100k entries)", device)
			}
		}

		// Phase 1: Parse all packets in the block WITHOUT holding the lock.
		// IP parsing, string conversion, and classification happen here.
		batch = batch[:0]
		_, err := ring.ReadBlock(func(pkt []byte, wireLen uint32) {
			ipPacket := packets.ParseIPPacket(pkt, true)
			if ipPacket.Version == 0 || ipPacket.IsTunnel && !t.direct {
				return
			}
			srcIP := ipPacket.SrcIP
			dstIP := ipPacket.DstIP
			srcStr := ipStr(srcIP)
			dstStr := ipStr(dstIP)
			_, srcSelf := t.selfIPs[srcStr]
			_, dstSelf := t.selfIPs[dstStr]

			ipVersion := "IPv4"
			if ipPacket.Version != 4 {
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
			transport := parseDirectTransport(pkt)

			batch = append(batch, parsedPkt{
				srcStr:            srcStr,
				dstStr:            dstStr,
				srcLocal:          t.packetIPIsLocal(srcIP),
				dstLocal:          t.packetIPIsLocal(dstIP),
				srcSelf:           srcSelf,
				dstSelf:           dstSelf,
				srcLoopLL:         srcIP.IsLoopback() || srcIP.IsLinkLocalUnicast(),
				dstLoopLL:         dstIP.IsLoopback() || dstIP.IsLinkLocalUnicast(),
				wireLen:           uint64(wireLen),
				proto:             proto,
				ipVersion:         ipVersion,
				srcPort:           transport.srcPort,
				dstPort:           transport.dstPort,
				hasPorts:          transport.hasPorts,
				transportProtocol: transport.protocol,
			})
		}, 100)
		if err != nil {
			return err
		}

		if len(batch) == 0 {
			continue
		}

		// Phase 2: Apply all parsed packets under ONE lock acquisition.
		// This is ~170 map updates per lock instead of 1, eliminating
		// lock contention with SSE readers.
		t.mu.Lock()
		if t.current == nil {
			t.mu.Unlock()
			continue
		}
		rSlot := t.rateRing[t.rateRingIdx]
		var directSlot *directRateSlot
		if t.direct {
			directSlot = t.directRateRing[t.directRateRingIdx]
		}
		for i := range batch {
			p := &batch[i]
			t.accountPacket(p, t.current, rSlot)
			t.accountDirection(p, t.current, rSlot)
			if t.direct {
				if t.accountDirectPeer(p, directSlot) {
					t.accountDirectFlow(p, directSlot)
				}
			}
			t.current.protoBytes[p.proto] += p.wireLen
			t.current.ipVerBytes[p.ipVersion] += p.wireLen
		}
		t.mu.Unlock()
	}
}

func (t *Tracker) packetIPIsLocal(ip net.IP) bool {
	if !t.direct {
		return netutil.IsLocal(ip, t.localNets)
	}
	if ip == nil {
		return false
	}
	for _, network := range t.localNets {
		if network != nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// accountDirectPeer records a packet once under its remote endpoint. Direction
// is relative to the actual local endpoint, including router-originated flows.
// Must be called with t.mu held.
func (t *Tracker) accountDirectPeer(p *parsedPkt, slot *directRateSlot) bool {
	if slot == nil || p.srcLocal == p.dstLocal {
		return false
	}
	var pair directPairKey
	var rx, tx uint64
	if p.srcLocal {
		pair = directPairKey{local: p.srcStr, remote: p.dstStr}
		tx = p.wireLen
	} else {
		pair = directPairKey{local: p.dstStr, remote: p.srcStr}
		rx = p.wireLen
	}
	t.directRxBytes = saturatingAddUint64(t.directRxBytes, rx)
	t.directTxBytes = saturatingAddUint64(t.directTxBytes, tx)
	acc, ok := slot.peers[pair]
	if !ok {
		if len(slot.peers) >= maxHostsPerBucket {
			return false
		}
		acc = &hostAccum{}
		slot.peers[pair] = acc
	}
	acc.bytes += p.wireLen
	acc.rxBytes += rx
	acc.txBytes += tx
	acc.packets++
	return true
}

func saturatingAddUint64(current, value uint64) uint64 {
	const maxUint64 = ^uint64(0)
	if value > maxUint64-current {
		return maxUint64
	}
	return current + value
}

// accountDirectFlow keeps remote service aggregation independent of the local
// ephemeral port. Non-initial fragments and protocols without ports use a
// protocol-specific unknown-port bucket.
func (t *Tracker) accountDirectFlow(p *parsedPkt, slot *directRateSlot) {
	if slot == nil || p.srcLocal == p.dstLocal {
		return
	}
	pair := directPairKey{}
	remotePort := uint16(0)
	var rx, tx uint64
	if p.srcLocal {
		pair = directPairKey{local: p.srcStr, remote: p.dstStr}
		remotePort = p.dstPort
		tx = p.wireLen
	} else {
		pair = directPairKey{local: p.dstStr, remote: p.srcStr}
		remotePort = p.srcPort
		rx = p.wireLen
	}
	key := directFlowKey{
		directPairKey: pair, protocol: p.transportProtocol,
		remotePort: remotePort, hasPort: p.hasPorts,
	}
	if key.protocol == "" {
		key.protocol = "IP-0"
	}
	if slot.flows == nil {
		slot.flows = make(map[directFlowKey]*hostAccum)
	}
	acc, ok := slot.flows[key]
	if !ok {
		if len(slot.flows) >= maxDirectFlowsPerSlot {
			return
		}
		acc = &hostAccum{}
		slot.flows[key] = acc
	}
	acc.bytes += p.wireLen
	acc.rxBytes += rx
	acc.txBytes += tx
	acc.packets++
}

// accountPacket updates host byte/packet counters in the current bucket and
// rate ring slot for both endpoints of a parsed packet.
// Must be called with t.mu held.
func (t *Tracker) accountPacket(p *parsedPkt, current *bucket, rSlot *rateSlot) {
	for _, entry := range []struct {
		ip     string
		loopLL bool
	}{
		{p.srcStr, p.srcLoopLL},
		{p.dstStr, p.dstLoopLL},
	} {
		if entry.loopLL {
			continue
		}
		if _, ok := current.hosts[entry.ip]; !ok {
			if len(current.hosts) >= maxHostsPerBucket {
				continue
			}
			current.hosts[entry.ip] = &hostAccum{}
		}
		current.hosts[entry.ip].bytes += p.wireLen
		current.hosts[entry.ip].packets++

		if rSlot != nil {
			if _, ok := rSlot.hosts[entry.ip]; !ok {
				rSlot.hosts[entry.ip] = &hostAccum{}
			}
			rSlot.hosts[entry.ip].bytes += p.wireLen
			rSlot.hosts[entry.ip].packets++
		}
	}
}

// accountDirection updates rx/tx byte counters based on local-net direction
// detection for a parsed packet.
// Must be called with t.mu held.
func (t *Tracker) accountDirection(p *parsedPkt, current *bucket, rSlot *rateSlot) {
	if len(t.localNets) == 0 {
		return
	}
	if p.srcLocal && !p.dstLocal {
		if h, ok := current.hosts[p.srcStr]; ok {
			h.txBytes += p.wireLen
		}
		if rSlot != nil {
			if h, ok := rSlot.hosts[p.srcStr]; ok {
				h.txBytes += p.wireLen
			}
		}
		if h, ok := current.hosts[p.dstStr]; ok {
			h.txBytes += p.wireLen
		}
		if rSlot != nil {
			if h, ok := rSlot.hosts[p.dstStr]; ok {
				h.txBytes += p.wireLen
			}
		}
		// local (src) -> remote (dst): upload from the local host's perspective.
		t.recordPair(current, p.srcStr, p.dstStr, p.wireLen, 0, p.wireLen)
	} else if !p.srcLocal && p.dstLocal {
		if h, ok := current.hosts[p.dstStr]; ok {
			h.rxBytes += p.wireLen
		}
		if rSlot != nil {
			if h, ok := rSlot.hosts[p.dstStr]; ok {
				h.rxBytes += p.wireLen
			}
		}
		if h, ok := current.hosts[p.srcStr]; ok {
			h.rxBytes += p.wireLen
		}
		// remote (src) -> local (dst): download from the local host's perspective.
		t.recordPair(current, p.dstStr, p.srcStr, p.wireLen, p.wireLen, 0)
		if rSlot != nil {
			if h, ok := rSlot.hosts[p.srcStr]; ok {
				h.rxBytes += p.wireLen
			}
		}
	} else if p.srcLocal && p.dstLocal {
		// For routed traffic between local networks, direction is relative to
		// each endpoint: the source uploads and the destination downloads.
		if h, ok := current.hosts[p.srcStr]; ok {
			h.txBytes += p.wireLen
		}
		if h, ok := current.hosts[p.dstStr]; ok {
			h.rxBytes += p.wireLen
		}
		if rSlot != nil {
			if h, ok := rSlot.hosts[p.srcStr]; ok {
				h.txBytes += p.wireLen
			}
			if h, ok := rSlot.hosts[p.dstStr]; ok {
				h.rxBytes += p.wireLen
			}
		}
	}
}

// recordPair updates the (local host, remote host) pair accumulator used by
// ASN/provider drill-downs (e.g. "which of my machines talk to AS15169?").
// Bounded by maxPairsPerBucket and maxRemotesPerLocalHost so a port-scanner
// or similar high-fanout traffic pattern can't cause unbounded growth.
//
// The router's own interface IPs (selfIPs) are excluded from the "local"
// side: the router itself isn't a LAN client, and its own traffic (DNS
// resolution, NTP, package updates, this app's own latency/speedtest
// probes, etc.) would otherwise show up as a single "machine" talking to
// nearly every ASN, drowning out actual client attribution. This mirrors
// the selfIPs exclusion already applied in TopByVolume/TopByBandwidth/
// TopByCountry/GetGeoBreakdown.
// Must be called with t.mu held.
func (t *Tracker) recordPair(current *bucket, localIP, remoteIP string, wireLen, rx, tx uint64) {
	if _, isSelf := t.selfIPs[localIP]; isSelf {
		return
	}
	remotes, ok := current.pairs[localIP]
	if !ok {
		if len(current.pairs) >= maxPairsPerBucket {
			return
		}
		remotes = make(map[string]*hostAccum)
		current.pairs[localIP] = remotes
	}
	acc, ok := remotes[remoteIP]
	if !ok {
		if len(remotes) >= maxRemotesPerLocalHost {
			return
		}
		acc = &hostAccum{}
		remotes[remoteIP] = acc
	}
	acc.bytes += wireLen
	acc.rxBytes += rx
	acc.txBytes += tx
	acc.packets++
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
				// Nil out old entries so the backing array doesn't
				// prevent GC of expired bucket data. Without this,
				// the pointers stay alive until append reallocates
				// the backing array — leaking up to 24h of buckets.
				for i := 0; i < idx; i++ {
					t.buckets[i] = nil
				}
				t.buckets = t.buckets[idx:]
			}
			t.current = &bucket{
				timestamp:  now.Truncate(bucketSize),
				hosts:      make(map[string]*hostAccum),
				protoBytes: make(map[string]uint64),
				ipVerBytes: make(map[string]uint64),
				pairs:      make(map[string]map[string]*hostAccum),
			}
			t.mu.Unlock()
		case <-t.stopCh:
			return
		}
	}
}

// rotateRateRing advances the short rate ring every rateSlotDuration.
func (t *Tracker) rotateRateRing() {
	ticker := time.NewTicker(rateSlotDuration)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			t.mu.Lock()
			t.rateRingIdx = (t.rateRingIdx + 1) % rateSlotCount
			t.rateRing[t.rateRingIdx] = &rateSlot{
				timestamp: time.Now(),
				hosts:     make(map[string]*hostAccum),
			}
			t.mu.Unlock()
		case <-t.stopCh:
			return
		}
	}
}

func (t *Tracker) rotateDirectRateRing() {
	ticker := time.NewTicker(directRateSlotDuration)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			t.mu.Lock()
			t.directRateRingIdx = (t.directRateRingIdx + 1) % directRateSlotCount
			t.directRateRing[t.directRateRingIdx] = &directRateSlot{
				timestamp: now,
				peers:     make(map[directPairKey]*hostAccum),
				flows:     make(map[directFlowKey]*hostAccum),
			}
			t.mu.Unlock()
		case <-t.stopCh:
			return
		}
	}
}

// rateFromRing computes per-IP rates from the rate ring (excluding the
// current slot which is still accumulating). Returns bytes/elapsed maps.
// Must be called with t.mu held (at least RLock).
func (t *Tracker) rateFromRing() (rates map[string]*hostAccum, elapsed float64) {
	now := time.Now()
	rates = make(map[string]*hostAccum)
	var oldest time.Time

	for i := 0; i < rateSlotCount; i++ {
		slot := t.rateRing[i]
		if slot == nil {
			continue
		}
		// Include all slots (the current one is still accumulating,
		// but including it keeps rates responsive to new bursts).
		if oldest.IsZero() || slot.timestamp.Before(oldest) {
			oldest = slot.timestamp
		}
		for ip, acc := range slot.hosts {
			if e, ok := rates[ip]; ok {
				e.bytes += acc.bytes
				e.rxBytes += acc.rxBytes
				e.txBytes += acc.txBytes
				e.packets += acc.packets
			} else {
				rates[ip] = &hostAccum{
					bytes:   acc.bytes,
					rxBytes: acc.rxBytes,
					txBytes: acc.txBytes,
					packets: acc.packets,
				}
			}
		}
	}

	elapsed = now.Sub(oldest).Seconds()
	if elapsed < 1 {
		elapsed = 1
	}
	return
}

// directRateFromRing computes rates for direct local/remote pairs.
// Must be called with t.mu held (at least RLock).
func (t *Tracker) directRateFromRing(now time.Time, window time.Duration) (map[directPairKey]*hostAccum, float64) {
	rates := make(map[directPairKey]*hostAccum)
	var oldest time.Time
	cutoff := now.Add(-window)
	for _, slot := range t.directRateRing {
		if slot == nil || slot.timestamp.Before(cutoff) {
			continue
		}
		if oldest.IsZero() || slot.timestamp.Before(oldest) {
			oldest = slot.timestamp
		}
		for pair, acc := range slot.peers {
			if total, ok := rates[pair]; ok {
				total.bytes += acc.bytes
				total.rxBytes += acc.rxBytes
				total.txBytes += acc.txBytes
				total.packets += acc.packets
			} else {
				rates[pair] = &hostAccum{
					bytes: acc.bytes, rxBytes: acc.rxBytes,
					txBytes: acc.txBytes, packets: acc.packets,
				}
			}
		}
	}
	elapsed := now.Sub(oldest).Seconds()
	if elapsed > window.Seconds() {
		elapsed = window.Seconds()
	}
	if elapsed < 1 {
		elapsed = 1
	}
	return rates, elapsed
}

func (t *Tracker) directFlowRateFromRing(now time.Time, window time.Duration) (map[directFlowKey]*hostAccum, float64) {
	rates := make(map[directFlowKey]*hostAccum)
	var oldest time.Time
	cutoff := now.Add(-window)
	for _, slot := range t.directRateRing {
		if slot == nil || slot.timestamp.Before(cutoff) {
			continue
		}
		if oldest.IsZero() || slot.timestamp.Before(oldest) {
			oldest = slot.timestamp
		}
		for flow, acc := range slot.flows {
			if total, ok := rates[flow]; ok {
				total.bytes += acc.bytes
				total.rxBytes += acc.rxBytes
				total.txBytes += acc.txBytes
				total.packets += acc.packets
			} else {
				rates[flow] = &hostAccum{
					bytes: acc.bytes, rxBytes: acc.rxBytes,
					txBytes: acc.txBytes, packets: acc.packets,
				}
			}
		}
	}
	elapsed := now.Sub(oldest).Seconds()
	if elapsed > window.Seconds() {
		elapsed = window.Seconds()
	}
	if elapsed < 1 {
		elapsed = 1
	}
	return rates, elapsed
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

// SetGeo implements geoip.GeoFields.
func (s *TalkerStat) SetGeo(country, countryName, city string, lat, lon float64, asn uint, asOrg string) {
	s.Country = country
	s.CountryName = countryName
	s.City = city
	s.Latitude = lat
	s.Longitude = lon
	s.ASN = asn
	s.ASOrg = asOrg
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
		// Skip local/private/self IPs — they have no GeoIP data and
		// inflate the "Unknown" category.
		parsedIP := net.ParseIP(ip)
		if parsedIP != nil && (parsedIP.IsLoopback() || netutil.IsLocal(parsedIP, t.localNets)) {
			continue
		}
		if _, isSelf := t.selfIPs[ip]; isSelf {
			continue
		}

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

// ASNRemoteStat holds a single remote IP's 24h traffic contribution within
// an ASN, nested under a MachineASNStat so the UI can show exact local<->
// remote IP pairs instead of just a per-machine sum.
type ASNRemoteStat struct {
	IP         string `json:"ip"`
	Hostname   string `json:"hostname,omitempty"`
	TotalBytes uint64 `json:"total_bytes"`
	RxBytes    uint64 `json:"rx_bytes"`
	TxBytes    uint64 `json:"tx_bytes"`
	Packets    uint64 `json:"packets"`
}

// MachineASNStat holds a local host's aggregated 24h traffic to a specific
// ASN/provider, powering the "which of my machines talk to AS X" drill-down.
type MachineASNStat struct {
	IP          string          `json:"ip"`
	Hostname    string          `json:"hostname,omitempty"`
	TotalBytes  uint64          `json:"total_bytes"`
	RxBytes     uint64          `json:"rx_bytes"`
	TxBytes     uint64          `json:"tx_bytes"`
	Packets     uint64          `json:"packets"`
	Connections int             `json:"connections"` // distinct remote IPs within the ASN
	Remotes     []ASNRemoteStat `json:"remotes"`     // per-remote-IP breakdown, sorted by bytes desc
}

// maxRemotesInResponse caps how many remote IPs are returned per local host
// in the ASN drill-down, so a host with hundreds of short-lived connections
// (e.g. to a CDN) doesn't bloat the response.
const maxRemotesInResponse = 50

// TopMachinesForASN returns the local hosts (by 24h total bytes) that have
// talked to the given ASN, along with each host's traffic totals toward it
// and a per-remote-IP breakdown. Unlike GetGeoBreakdown (which only totals
// bytes per-ASN with no local-host attribution), this walks the local->remote
// pair accumulators built by recordPair so it can answer "who on my network
// talks to this provider, and which specific remote IPs".
func (t *Tracker) TopMachinesForASN(asn uint, n int) []MachineASNStat {
	if t.geoDB == nil || !t.geoDB.Available() || asn == 0 {
		return nil
	}

	// Step 1: merge the per-bucket (local -> remote) pair totals under lock.
	t.mu.RLock()
	agg := make(map[string]map[string]*hostAccum)
	merge := func(pairs map[string]map[string]*hostAccum) {
		for local, remotes := range pairs {
			lm, ok := agg[local]
			if !ok {
				lm = make(map[string]*hostAccum, len(remotes))
				agg[local] = lm
			}
			for remote, acc := range remotes {
				a, ok := lm[remote]
				if !ok {
					a = &hostAccum{}
					lm[remote] = a
				}
				a.bytes += acc.bytes
				a.rxBytes += acc.rxBytes
				a.txBytes += acc.txBytes
				a.packets += acc.packets
			}
		}
	}
	for _, b := range t.buckets {
		merge(b.pairs)
	}
	if t.current != nil {
		merge(t.current.pairs)
	}
	t.mu.RUnlock()

	// Step 2: GeoIP lookups outside the lock — filter remotes to the wanted
	// ASN, keeping the per-remote breakdown (not just a sum) per local host.
	type acc struct {
		bytes, rx, tx, packets uint64
		remotes                map[string]*hostAccum
	}
	byHost := make(map[string]*acc)
	for local, remotes := range agg {
		for remote, a := range remotes {
			geo := t.geoDB.Lookup(remote)
			if geo == nil || geo.ASN != asn {
				continue
			}
			h, ok := byHost[local]
			if !ok {
				h = &acc{remotes: make(map[string]*hostAccum)}
				byHost[local] = h
			}
			h.bytes += a.bytes
			h.rx += a.rxBytes
			h.tx += a.txBytes
			h.packets += a.packets
			h.remotes[remote] = a
		}
	}

	list := make([]MachineASNStat, 0, len(byHost))
	for ip, h := range byHost {
		remotes := make([]ASNRemoteStat, 0, len(h.remotes))
		for remoteIP, a := range h.remotes {
			remotes = append(remotes, ASNRemoteStat{
				IP:         remoteIP,
				TotalBytes: a.bytes,
				RxBytes:    a.rxBytes,
				TxBytes:    a.txBytes,
				Packets:    a.packets,
			})
		}
		sort.Slice(remotes, func(i, j int) bool {
			return remotes[i].TotalBytes > remotes[j].TotalBytes
		})
		if len(remotes) > maxRemotesInResponse {
			remotes = remotes[:maxRemotesInResponse]
		}
		list = append(list, MachineASNStat{
			IP:          ip,
			TotalBytes:  h.bytes,
			RxBytes:     h.rx,
			TxBytes:     h.tx,
			Packets:     h.packets,
			Connections: len(h.remotes),
			Remotes:     remotes,
		})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].TotalBytes > list[j].TotalBytes
	})
	if len(list) > n {
		list = list[:n]
	}

	// Step 3: Hostname (DNS) enrichment only for the trimmed result — both
	// for the local host itself and for each of its listed remote IPs.
	for i := range list {
		if t.dns != nil {
			list[i].Hostname = t.dns.LookupAddrAsync(list[i].IP)
			for j := range list[i].Remotes {
				list[i].Remotes[j].Hostname = t.dns.LookupAddrAsync(list[i].Remotes[j].IP)
			}
		}
	}
	return list
}

// BucketPoint is a single 1-minute data point for a host.
type BucketPoint struct {
	Timestamp int64  `json:"ts"`
	Bytes     uint64 `json:"bytes"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	Packets   uint64 `json:"packets"`
}

// HostHistory returns the per-minute bandwidth history for a single IP
// over the 24h window. Returns nil if the IP has never been seen.
func (t *Tracker) HostHistory(ip string) []BucketPoint {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var points []BucketPoint
	for _, b := range t.buckets {
		if acc, ok := b.hosts[ip]; ok {
			points = append(points, BucketPoint{
				Timestamp: b.timestamp.UnixMilli(),
				Bytes:     acc.bytes,
				RxBytes:   acc.rxBytes,
				TxBytes:   acc.txBytes,
				Packets:   acc.packets,
			})
		}
	}
	if t.current != nil {
		if acc, ok := t.current.hosts[ip]; ok {
			points = append(points, BucketPoint{
				Timestamp: t.current.timestamp.UnixMilli(),
				Bytes:     acc.bytes,
				RxBytes:   acc.rxBytes,
				TxBytes:   acc.txBytes,
				Packets:   acc.packets,
			})
		}
	}
	return points
}

// HostTotals returns the aggregate traffic stats for a single IP.
// Returns nil if the IP has never been seen.
func (t *Tracker) HostTotals(ip string) *TalkerStat {
	t.mu.RLock()
	var found bool
	stat := &TalkerStat{IP: ip}
	for _, b := range t.buckets {
		if acc, ok := b.hosts[ip]; ok {
			found = true
			stat.TotalBytes += acc.bytes
			stat.RxBytes += acc.rxBytes
			stat.TxBytes += acc.txBytes
			stat.Packets += acc.packets
		}
	}
	if t.current != nil {
		if acc, ok := t.current.hosts[ip]; ok {
			found = true
			stat.TotalBytes += acc.bytes
			stat.RxBytes += acc.rxBytes
			stat.TxBytes += acc.txBytes
			stat.Packets += acc.packets
		}
		// Rate from the short rate ring (5s slots, ~30s window)
		rates, elapsed := t.rateFromRing()
		if r, ok := rates[ip]; ok {
			stat.RateBytes = float64(r.bytes) / elapsed
			stat.RxRate = float64(r.rxBytes) / elapsed
			stat.TxRate = float64(r.txBytes) / elapsed
		}
	}
	t.mu.RUnlock()

	if !found {
		return nil
	}

	// Enrich outside lock
	if t.dns != nil {
		stat.Hostname = t.dns.LookupAddr(ip)
	}
	t.geoDB.Enrich(stat.IP, stat)
	return stat
}

// UniqueIPs returns the number of distinct external IPs seen in the 24h window.
func (t *Tracker) UniqueIPs() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	seen := make(map[string]struct{})
	for _, b := range t.buckets {
		for ip := range b.hosts {
			seen[ip] = struct{}{}
		}
	}
	if t.current != nil {
		for ip := range t.current.hosts {
			seen[ip] = struct{}{}
		}
	}
	return len(seen)
}
