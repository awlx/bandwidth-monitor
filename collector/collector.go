package collector

import (
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bandwidth-monitor/poller"

	vnl "github.com/vishvananda/netlink"
)

type InterfaceStat struct {
	Name            string   `json:"name"`
	IfaceType       string   `json:"iface_type"`
	OperState       string   `json:"oper_state"`
	Addrs           []string `json:"addrs,omitempty"`
	WAN             bool     `json:"wan,omitempty"`
	VPNRouting      bool     `json:"vpn_routing"`
	VPNRoutingSince string   `json:"vpn_routing_since,omitempty"`
	VPNTracked      bool     `json:"vpn_tracked"`
	Speed           int      `json:"speed,omitempty"`
	RxBytes         uint64   `json:"rx_bytes"`
	TxBytes         uint64   `json:"tx_bytes"`
	RxPackets       uint64   `json:"rx_packets"`
	TxPackets       uint64   `json:"tx_packets"`
	RxErrors        uint64   `json:"rx_errors"`
	TxErrors        uint64   `json:"tx_errors"`
	RxDropped       uint64   `json:"rx_dropped"`
	TxDropped       uint64   `json:"tx_dropped"`
	RxRate          float64  `json:"rx_rate"`
	TxRate          float64  `json:"tx_rate"`
	RxPPS           float64  `json:"rx_pps"`
	TxPPS           float64  `json:"tx_pps"`
	RxErrorRate     float64  `json:"rx_error_rate"`
	TxErrorRate     float64  `json:"tx_error_rate"`
	RxDropRate      float64  `json:"rx_drop_rate"`
	TxDropRate      float64  `json:"tx_drop_rate"`
	Timestamp       int64    `json:"timestamp"`
}

type HistoryPoint struct {
	Timestamp int64   `json:"t"`
	RxRate    float64 `json:"rx"`
	TxRate    float64 `json:"tx"`
}

const (
	pollInterval   = 1 * time.Second
	historyMaxAge  = 24 * time.Hour
	historyPruneAt = 86400
)

type Collector struct {
	mu             sync.RWMutex
	current        map[string]*InterfaceStat
	previous       map[string]*rawStat
	history        map[string][]HistoryPoint
	ifaceTypeCache map[string]string
	vpnStatusFiles map[string]string // iface name → sentinel file path
	allowedIfaces  map[string]bool   // nil = all; non-nil = whitelist
	wanIfaces      map[string]bool   // nil = auto-detect; non-nil = explicit WAN interfaces
	nlHandle       *vnl.Handle       // persistent netlink handle
	addrCache      map[int][]string  // cached addresses by link index
	addrCacheTime  time.Time         // last address refresh
	span           *spanOverlay      // nil when SPAN mode is disabled
	spanPrevRx     uint64
	spanPrevTx     uint64
	spanPrevTs     time.Time // timestamp of the last SPAN snapshot (own baseline)
	spanHasPrev    bool
	poller.Runner
}

type rawStat struct {
	rxBytes   uint64
	txBytes   uint64
	rxPackets uint64
	txPackets uint64
	rxErrors  uint64
	txErrors  uint64
	rxDropped uint64
	txDropped uint64
	ts        time.Time
}

// NewForTest returns a Collector pre-seeded with history and no netlink handle. Only for use
// in tests of packages that consume the collector (e.g. handler); never poll it.
func NewForTest(history map[string][]HistoryPoint) *Collector {
	if history == nil {
		history = make(map[string][]HistoryPoint)
	}
	return &Collector{history: history}
}

func New(vpnStatusFiles map[string]string, allowedIfaces []string, wanIfaces []string) *Collector {
	if vpnStatusFiles == nil {
		vpnStatusFiles = make(map[string]string)
	}
	var allowed map[string]bool
	if len(allowedIfaces) > 0 {
		allowed = make(map[string]bool, len(allowedIfaces))
		for _, name := range allowedIfaces {
			allowed[name] = true
		}
	}
	var wanSet map[string]bool
	if len(wanIfaces) > 0 {
		wanSet = make(map[string]bool, len(wanIfaces))
		for _, name := range wanIfaces {
			wanSet[name] = true
		}
	}
	// Create a persistent netlink handle to avoid per-poll socket creation.
	// Falls back to package-level functions if handle creation fails.
	nlh, err := vnl.NewHandle(vnl.FAMILY_ALL)
	if err != nil {
		log.Printf("collector: failed to create persistent netlink handle: %v (will use per-call sockets)", err)
	}
	c := &Collector{
		current:        make(map[string]*InterfaceStat),
		previous:       make(map[string]*rawStat),
		history:        make(map[string][]HistoryPoint),
		ifaceTypeCache: make(map[string]string),
		vpnStatusFiles: vpnStatusFiles,
		allowedIfaces:  allowed,
		wanIfaces:      wanSet,
		nlHandle:       nlh,
		addrCache:      make(map[int][]string),
	}
	c.Runner.Init()
	return c
}

// EnableSPAN activates pcap-based direction detection on a SPAN/mirror port.
// When enabled, the RX/TX rates and cumulative bytes for the named device are
// derived from packet inspection against localNets instead of kernel counters.
func (c *Collector) EnableSPAN(device string, promiscuous bool, localNets []*net.IPNet) {
	c.span = newSpanOverlay(device, promiscuous, localNets)
}

func (c *Collector) Run() {
	if c.span != nil {
		go c.span.run()
	}
	c.Runner.Run(pollInterval, c.poll)
}

func (c *Collector) Stop() {
	c.Runner.Stop()
	if c.span != nil {
		c.span.stop()
	}
	if c.nlHandle != nil {
		c.nlHandle.Close()
	}
}

func (c *Collector) GetAll() []InterfaceStat {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stats := make([]InterfaceStat, 0, len(c.current))
	for _, s := range c.current {
		if c.allowedIfaces != nil && !c.allowedIfaces[s.Name] {
			continue
		}
		stats = append(stats, *s)
	}
	return stats
}

func (c *Collector) GetHistory() map[string][]HistoryPoint {
	return c.GetHistoryFiltered("", 0)
}

// GetHistoryFiltered returns per-interface history, optionally narrowed to a single interface
// (iface != "") and/or to points at or after sinceMillis (Unix milliseconds, > 0). Zero values
// mean no filtering, so GetHistoryFiltered("", 0) is equivalent to GetHistory.
func (c *Collector) GetHistoryFiltered(iface string, sinceMillis int64) map[string][]HistoryPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make(map[string][]HistoryPoint, len(c.history))
	for k, v := range c.history {
		if iface != "" && k != iface {
			continue
		}
		if c.allowedIfaces != nil && !c.allowedIfaces[k] {
			continue
		}
		if sinceMillis > 0 {
			// Timestamps are append-only ascending, so binary-search the first point in range.
			v = v[sort.Search(len(v), func(i int) bool { return v[i].Timestamp >= sinceMillis }):]
		}
		cp := make([]HistoryPoint, len(v))
		copy(cp, v)
		result[k] = cp
	}
	return result
}

// SparkPoint is a lightweight rate pair for sparkline rendering.
type SparkPoint struct {
	RX float64 `json:"rx"`
	TX float64 `json:"tx"`
}

// GetSparklines returns the last `duration` of per-interface rate data,
// downsampled to at most `maxPoints` points.
func (c *Collector) GetSparklines(duration time.Duration, maxPoints int) map[string][]SparkPoint {
	c.mu.RLock()
	defer c.mu.RUnlock()
	cutoff := time.Now().Add(-duration).UnixMilli()
	result := make(map[string][]SparkPoint, len(c.history))

	for name, hist := range c.history {
		if c.allowedIfaces != nil && !c.allowedIfaces[name] {
			continue
		}
		start := 0
		for start < len(hist) && hist[start].Timestamp < cutoff {
			start++
		}
		pts := hist[start:]
		if len(pts) == 0 {
			continue
		}

		if len(pts) <= maxPoints {
			sp := make([]SparkPoint, len(pts))
			for i, p := range pts {
				sp[i] = SparkPoint{RX: p.RxRate, TX: p.TxRate}
			}
			result[name] = sp
		} else {
			sp := make([]SparkPoint, maxPoints)
			step := float64(len(pts)) / float64(maxPoints)
			for i := 0; i < maxPoints; i++ {
				idx := int(float64(i) * step)
				if idx >= len(pts) {
					idx = len(pts) - 1
				}
				sp[i] = SparkPoint{RX: pts[idx].RxRate, TX: pts[idx].TxRate}
			}
			result[name] = sp
		}
	}
	return result
}

// linkInfo holds everything we extract from a single RTM_GETLINK response.
type linkInfo struct {
	name      string
	operState string
	ifType    string // classified type: physical, vpn, vlan, ppp, loopback, span
	encapType string // ARPHRD text form: "ether", "loopback", "none", "ppp", etc.
	linkKind  string // IFLA_INFO_KIND: wireguard, vlan, bridge, bond, gre, ...
	speed     int    // link speed in Mbps (0 = unknown)
	stats     *rawStat
	addrs     []string
}

const addrCacheTTL = 10 * time.Second

func (c *Collector) poll() {
	var links []vnl.Link
	var err error
	if c.nlHandle != nil {
		links, err = c.nlHandle.LinkList()
	} else {
		links, err = vnl.LinkList()
	}
	if err != nil {
		log.Printf("collector: netlink LinkList: %v", err)
		return
	}

	// Refresh address cache every 10 seconds (addresses rarely change)
	if time.Since(c.addrCacheTime) >= addrCacheTTL {
		var allAddrs []vnl.Addr
		if c.nlHandle != nil {
			allAddrs, err = c.nlHandle.AddrList(nil, vnl.FAMILY_ALL)
		} else {
			allAddrs, err = vnl.AddrList(nil, vnl.FAMILY_ALL)
		}
		if err != nil {
			log.Printf("collector: netlink AddrList: %v", err)
		} else {
			addrsByIndex := make(map[int][]string)
			for _, a := range allAddrs {
				addrsByIndex[a.LinkIndex] = append(addrsByIndex[a.LinkIndex], a.IPNet.String())
			}
			c.addrCache = addrsByIndex
			c.addrCacheTime = time.Now()
		}
	}

	// Build linkInfo map from netlink data
	infos := make(map[string]*linkInfo, len(links))
	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}
		name := attrs.Name

		li := &linkInfo{
			name:      name,
			operState: operStateStr(attrs.OperState),
			encapType: attrs.EncapType,
			addrs:     c.addrCache[attrs.Index],
		}

		// Link speed (Mbps) — read from sysfs. Returns 0 for virtual interfaces.
		li.speed = readLinkSpeed(name)

		// Extract IFLA_INFO_KIND via link type name
		li.linkKind = link.Type()

		// Get stats from IFLA_STATS64 (vishvananda populates attrs.Statistics)
		if s := attrs.Statistics; s != nil {
			li.stats = &rawStat{
				rxBytes:   s.RxBytes,
				txBytes:   s.TxBytes,
				rxPackets: s.RxPackets,
				txPackets: s.TxPackets,
				rxErrors:  s.RxErrors,
				txErrors:  s.TxErrors,
				rxDropped: s.RxDropped,
				txDropped: s.TxDropped,
			}
		} else {
			li.stats = &rawStat{}
		}

		// Classify interface type
		li.ifType = c.classifyLink(li)

		infos[name] = li
	}

	// Read VPN status files outside the lock (file I/O)
	vpnState := make(map[string]struct {
		routing bool
		since   string
	})
	for name := range infos {
		routing, since := c.checkVPNRouting(name)
		vpnState[name] = struct {
			routing bool
			since   string
		}{routing, since}
	}

	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove interfaces that no longer exist
	for name := range c.current {
		if _, exists := infos[name]; !exists {
			delete(c.current, name)
			delete(c.previous, name)
			// Drop the SPAN baseline too, so a returning SPAN device
			// re-baselines instead of reporting a rate spanning its downtime.
			if c.span != nil && name == c.span.device {
				c.spanHasPrev = false
			}
		}
	}

	// Determine the fastest physical link speed — used as the rate cap for
	// virtual interfaces (VPN, bridges) that don't report their own speed.
	maxPhysSpeed := 0
	for _, li := range infos {
		if li.speed > maxPhysSpeed {
			maxPhysSpeed = li.speed
		}
	}

	for name, li := range infos {
		cur := li.stats
		prev, hasPrev := c.previous[name]
		vs := vpnState[name]
		_, vpnTracked := c.vpnStatusFiles[name]
		iface := &InterfaceStat{
			Name:            name,
			IfaceType:       li.ifType,
			OperState:       li.operState,
			Addrs:           li.addrs,
			VPNRouting:      vs.routing,
			VPNRoutingSince: vs.since,
			VPNTracked:      vpnTracked,
			Speed:           li.speed,
			RxBytes:         cur.rxBytes,
			TxBytes:         cur.txBytes,
			RxPackets:       cur.rxPackets,
			TxPackets:       cur.txPackets,
			RxErrors:        cur.rxErrors,
			TxErrors:        cur.txErrors,
			RxDropped:       cur.rxDropped,
			TxDropped:       cur.txDropped,
			Timestamp:       now.UnixMilli(),
		}
		iface.WAN = c.isWAN(iface)

		if hasPrev {
			dt := now.Sub(prev.ts).Seconds()
			if dt > 0 {
				iface.RxRate = safeRate(cur.rxBytes, prev.rxBytes, dt)
				iface.TxRate = safeRate(cur.txBytes, prev.txBytes, dt)
				iface.RxPPS = safeRate(cur.rxPackets, prev.rxPackets, dt)
				iface.TxPPS = safeRate(cur.txPackets, prev.txPackets, dt)
				iface.RxErrorRate = safeRate(cur.rxErrors, prev.rxErrors, dt)
				iface.TxErrorRate = safeRate(cur.txErrors, prev.txErrors, dt)
				iface.RxDropRate = safeRate(cur.rxDropped, prev.rxDropped, dt)
				iface.TxDropRate = safeRate(cur.txDropped, prev.txDropped, dt)

				// Reject impossibly high byte rates caused by hardware
				// offload counter flushes or 32-bit counter wraparound.
				effSpeed := li.speed
				if effSpeed == 0 {
					effSpeed = maxPhysSpeed
				}
				maxBytesPerSec := rateLimit(effSpeed)
				if iface.RxRate > maxBytesPerSec {
					iface.RxRate = 0
				}
				if iface.TxRate > maxBytesPerSec {
					iface.TxRate = 0
				}
			}
		}

		// SPAN overlay: override RX/TX with direction-aware pcap data
		if c.span != nil && name == c.span.device {
			rxB, txB, rxP, txP := c.span.snapshot()
			iface.RxBytes = rxB
			iface.TxBytes = txB
			iface.RxPackets = rxP
			iface.TxPackets = txP
			iface.IfaceType = "span"
			// Use the SPAN's own previous timestamp rather than the kernel
			// rawStat's: prev is nil on the first poll and whenever the SPAN
			// device has just (re)appeared after a flap (its entry is dropped
			// from c.previous below), which would panic on prev.ts.
			if c.spanHasPrev {
				dt := now.Sub(c.spanPrevTs).Seconds()
				if dt > 0 {
					iface.RxRate = float64(rxB-c.spanPrevRx) / dt
					iface.TxRate = float64(txB-c.spanPrevTx) / dt
				}
			}
			c.spanPrevRx = rxB
			c.spanPrevTx = txB
			c.spanPrevTs = now
			c.spanHasPrev = true
		}

		c.current[name] = iface
		cur.ts = now
		c.previous[name] = cur

		if hasPrev {
			c.history[name] = append(c.history[name], HistoryPoint{
				Timestamp: now.UnixMilli(),
				RxRate:    iface.RxRate,
				TxRate:    iface.TxRate,
			})
			if len(c.history[name]) > historyPruneAt {
				cutoff := now.Add(-historyMaxAge).UnixMilli()
				idx := 0
				for idx < len(c.history[name]) && c.history[name][idx].Timestamp < cutoff {
					idx++
				}
				c.history[name] = c.history[name][idx:]
			}
		}
	}
}

// classifyLink determines the interface category from netlink data.
// Uses IFLA_INFO_KIND (linkKind), ARPHRD type (arphrd), and name-based
// heuristics as fallback. Returns one of:
// "physical", "vlan", "ppp", "vpn", "loopback".
func (c *Collector) classifyLink(li *linkInfo) string {
	// Check cache first
	if t, ok := c.ifaceTypeCache[li.name]; ok {
		return t
	}

	t := classifyLinkUncached(li)
	c.ifaceTypeCache[li.name] = t
	return t
}

// readLinkSpeed reads the negotiated link speed in Mbps from sysfs.
// Returns 0 for virtual interfaces or if the file is unreadable.
func readLinkSpeed(name string) int {
	data, err := os.ReadFile("/sys/class/net/" + name + "/speed")
	if err != nil {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func classifyLinkUncached(li *linkInfo) string {
	// 1. IFLA_INFO_KIND — the most reliable classifier
	switch li.linkKind {
	case "wireguard":
		return "vpn"
	case "vlan":
		return "vlan"
	case "bridge":
		return "physical" // group bridges with physical
	case "bond":
		return "physical" // group bonds with physical
	case "ppp", "pppoe":
		return "ppp"
	case "gre", "gretap", "ip6gre", "ip6gretap", "ip6tnl", "ipip", "sit", "vti", "vti6":
		return "vpn"
	case "tun", "tap":
		return "vpn"
	case "vxlan", "geneve":
		return "vpn"
	}

	// 2. ARPHRD encap type (string form from netlink)
	switch li.encapType {
	case "loopback":
		return "loopback"
	case "none": // ARPHRD_NONE — common for WireGuard and tunnels
		return "vpn"
	case "ppp":
		return "ppp"
	}

	// 3. Name-based fallback
	n := strings.ToLower(li.name)
	if strings.HasPrefix(n, "tun") || strings.HasPrefix(n, "tap") {
		return "vpn"
	}
	if strings.HasPrefix(n, "ppp") || strings.HasPrefix(n, "wwan") || strings.HasPrefix(n, "lte") {
		return "ppp"
	}
	if strings.Contains(n, ".") {
		return "vlan"
	}

	return "physical"
}

// isWAN reports whether the given interface looks like a WAN uplink.
// It checks (in order):
//  1. Explicit WAN_INTERFACE override (set via env var)
//  2. PPP type
//  3. Whether any assigned IPv4 address is publicly-routable
//  4. Whether the interface carries the IPv4 default route (handles double-NAT)
//
// IPv6 is intentionally ignored: with prefix delegation, LAN interfaces
// commonly carry globally-routable IPv6 addresses.
func (c *Collector) isWAN(iface *InterfaceStat) bool {
	// Explicit override takes priority
	if c.wanIfaces != nil {
		return c.wanIfaces[iface.Name]
	}
	if iface.IfaceType == "ppp" {
		return true
	}
	for _, a := range iface.Addrs {
		if isPublicIPv4(a) {
			return true
		}
	}
	if hasDefaultRoute(iface.Name) {
		return true
	}
	return false
}

// IsWAN is a standalone version for use outside the Collector (no override).
func IsWAN(iface *InterfaceStat) bool {
	if iface.IfaceType == "ppp" {
		return true
	}
	for _, a := range iface.Addrs {
		if isPublicIPv4(a) {
			return true
		}
	}
	if hasDefaultRoute(iface.Name) {
		return true
	}
	return false
}

// hasDefaultRoute reports whether the named interface carries an IPv4 default route.
func hasDefaultRoute(ifaceName string) bool {
	routes, err := vnl.RouteList(nil, vnl.FAMILY_V4)
	if err != nil {
		return false
	}
	for _, r := range routes {
		// Default route: Dst is nil or 0.0.0.0/0
		if r.Dst != nil {
			ones, bits := r.Dst.Mask.Size()
			if ones != 0 || bits == 0 {
				continue
			}
		}
		link, err := vnl.LinkByIndex(r.LinkIndex)
		if err != nil {
			continue
		}
		if link.Attrs().Name == ifaceName {
			return true
		}
	}
	return false
}

// isPublicIPv4 returns true when the CIDR string contains a public IPv4
// address — i.e. not RFC1918, not link-local, not loopback, not unspecified.
// IPv6 addresses always return false.
func isPublicIPv4(cidr string) bool {
	ipStr := cidr
	if idx := strings.IndexByte(cidr, '/'); idx != -1 {
		ipStr = cidr[:idx]
	}
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	// Only consider IPv4
	if ip.To4() == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsPrivate() || ip.IsUnspecified() {
		return false
	}
	return true
}

// operStateStr converts a netlink OperState to a human-readable string.
func operStateStr(state vnl.LinkOperState) string {
	switch state {
	case vnl.OperUp:
		return "up"
	case vnl.OperDown:
		return "down"
	case vnl.OperLowerLayerDown:
		return "lowerlayerdown"
	case vnl.OperTesting:
		return "testing"
	case vnl.OperDormant:
		return "dormant"
	case vnl.OperNotPresent:
		return "notpresent"
	default:
		return "unknown"
	}
}

// checkVPNRouting checks whether traffic is actively routed through a VPN interface
// by looking for a sentinel file configured via VPN_STATUS_FILES.
func (c *Collector) checkVPNRouting(name string) (bool, string) {
	path, ok := c.vpnStatusFiles[name]
	if !ok {
		return false, ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, ""
	}
	return true, strings.TrimSpace(string(data))
}

// safeRate computes (cur-prev)/dt, returning 0 if the counter went backwards
// (reset/wraparound).
func safeRate(cur, prev uint64, dt float64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur-prev) / dt
}

// rateLimit returns the maximum plausible byte rate for an interface.
// For interfaces with a known link speed, allows 50% headroom above the
// negotiated speed to tolerate measurement jitter. For virtual/unknown
// interfaces, uses a generous 100 Gbps cap.
// This filters out impossible spikes caused by hardware flow-offload counter
// flushes and 32-bit counter wraparound on embedded devices.
func rateLimit(speedMbps int) float64 {
	if speedMbps > 0 {
		return float64(speedMbps) * 1e6 / 8 * 1.5
	}
	return 100e9 / 8 // 100 Gbps
}
