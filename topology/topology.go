// Package topology discovers local network clients and attempts to build
// an approximate network graph from ARP/NDP tables, LLDP neighbors, and
// optional Unifi/Omada controller data.
package topology

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	vnl "github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"

	"bandwidth-monitor/netutil"
	"bandwidth-monitor/poller"
	"bandwidth-monitor/resolver"
	"bandwidth-monitor/wifi"
)

// NodeType classifies a discovered node.
type NodeType string

const (
	NodeSwitch  NodeType = "switch"
	NodeAP      NodeType = "ap"
	NodeClient  NodeType = "client"
	NodeGateway NodeType = "gateway"
	NodeSelf    NodeType = "self"
	NodeWANGW   NodeType = "wan_gw"
	NodeTunnel  NodeType = "tunnel"
)

// LinkType classifies the connection between two nodes.
type LinkType string

const (
	LinkWired    LinkType = "wired"
	LinkWireless LinkType = "wireless"
	LinkLLDP     LinkType = "lldp"
	LinkTunnel   LinkType = "tunnel"
	LinkWAN      LinkType = "wan"
)

// Node represents a discovered network entity.
type Node struct {
	ID       string   `json:"id"`
	MAC         string   `json:"mac"`
	IPs         []string `json:"ips"`
	Hostname    string   `json:"hostname"`
	Vendor      string   `json:"vendor"`
	Type        NodeType `json:"type"`
	DeviceClass string   `json:"device_class,omitempty"`
	DevCat      int      `json:"-"` // UniFi device category (internal, not serialized)
	SSID        string   `json:"ssid,omitempty"`
	Signal      int      `json:"signal,omitempty"`
	Radio       string   `json:"radio,omitempty"`
	Model       string   `json:"model,omitempty"`
	APName      string   `json:"ap_name,omitempty"`
	Iface       string   `json:"iface,omitempty"`
	State       string   `json:"state,omitempty"`
	Source      string   `json:"source"`
	LastSeen    int64    `json:"last_seen,omitempty"`
}

// Link represents a connection between two nodes.
type Link struct {
	SourceID string   `json:"source"`
	TargetID string   `json:"target"`
	Type     LinkType `json:"type"`
	Label    string   `json:"label,omitempty"`
}

// Overview is the top-level topology response.
type Overview struct {
	Nodes     []Node `json:"nodes"`
	Links     []Link `json:"links"`
	SelfNode  string `json:"self_node"`
	Gateway   string `json:"gateway,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// Scanner periodically discovers the local network.
type Scanner struct {
	mu           sync.RWMutex
	overview     *Overview
	dns          *resolver.Resolver
	wifiProvider wifi.Provider
	localNets    []*net.IPNet
	poller.Runner
	interval  time.Duration
	wanIfaces func() []string // returns names of WAN interfaces
}

// New creates a topology scanner.
func New(dns *resolver.Resolver, wifiProvider wifi.Provider, localNets []*net.IPNet, interval time.Duration) *Scanner {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	s := &Scanner{
		dns:          dns,
		wifiProvider: wifiProvider,
		localNets:    localNets,
		interval:     interval,
	}
	s.Runner.Init()
	return s
}

// SetWANInterfacesFunc sets the callback used to determine WAN interface names.
// Typically wired up from the collector: func() []string that returns interface
// names with WAN==true.
func (s *Scanner) SetWANInterfacesFunc(fn func() []string) {
	s.wanIfaces = fn
}

// Run starts the periodic scan loop. Call in a goroutine.
func (s *Scanner) Run() { s.Runner.Run(s.interval, s.scan) }

// GetOverview returns the latest topology snapshot.
func (s *Scanner) GetOverview() *Overview {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.overview
}

func (s *Scanner) scan() {
	nodeMap := make(map[string]*Node)
	linkSet := make(map[string]*Link)

	selfNode := s.discoverSelf(nodeMap)
	gwNode := s.discoverGateway(nodeMap, selfNode)
	s.discoverWAN(nodeMap, linkSet, selfNode)
	s.readNeighTable(nodeMap, vnl.FAMILY_V4, "arp")
	s.readNeighTable(nodeMap, vnl.FAMILY_V6, "ndp")
	s.readLLDP(nodeMap, linkSet)
	s.mergeWiFiController(nodeMap, linkSet)
	s.resolveHostnames(nodeMap)
	classifyDevices(nodeMap)
	s.inferLinks(nodeMap, linkSet, selfNode, gwNode)

	now := time.Now().UnixMilli()
	nodes := make([]Node, 0, len(nodeMap))
	for _, n := range nodeMap {
		if n.LastSeen == 0 {
			n.LastSeen = now
		}
		nodes = append(nodes, *n)
	}
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Type != nodes[j].Type {
			return nodeTypeOrder(nodes[i].Type) < nodeTypeOrder(nodes[j].Type)
		}
		return nodes[i].ID < nodes[j].ID
	})

	links := make([]Link, 0, len(linkSet))
	for _, l := range linkSet {
		links = append(links, *l)
	}

	gwID := ""
	if gwNode != nil {
		gwID = gwNode.ID
	}

	ov := &Overview{
		Nodes:     nodes,
		Links:     links,
		SelfNode:  selfNode,
		Gateway:   gwID,
		Timestamp: now,
	}

	s.mu.Lock()
	s.overview = ov
	s.mu.Unlock()
}

func nodeTypeOrder(t NodeType) int {
	switch t {
	case NodeTunnel:
		return 0
	case NodeWANGW:
		return 1
	case NodeGateway:
		return 2
	case NodeSwitch:
		return 4
	case NodeAP:
		return 5
	case NodeSelf:
		return 6
	case NodeClient:
		return 7
	default:
		return 9
	}
}

// ── Self discovery ────────────────────────────────────────────────

func (s *Scanner) discoverSelf(nodeMap map[string]*Node) string {
	hostname, _ := os.Hostname()
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var selfMAC string
	var selfIPs []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if iface.HardwareAddr == nil || len(iface.HardwareAddr) == 0 {
			continue
		}
		if selfMAC == "" {
			selfMAC = iface.HardwareAddr.String()
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
			if ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLinkLocalMulticast() || ipnet.IP.IsLoopback() {
				continue
			}
			selfIPs = append(selfIPs, ipnet.IP.String())
		}
	}
	if selfMAC == "" {
		selfMAC = "self"
	}
	nodeMap[selfMAC] = &Node{
		ID:       selfMAC,
		MAC:      selfMAC,
		IPs:      selfIPs,
		Hostname: hostname,
		Type:     NodeSelf,
		Source:   "local",
	}
	return selfMAC
}

// ── Gateway discovery ─────────────────────────────────────────────

func (s *Scanner) discoverGateway(nodeMap map[string]*Node, selfNode string) *Node {
	gwIP := defaultGatewayIP()
	if gwIP == "" || gwIP == "0.0.0.0" {
		if self, ok := nodeMap[selfNode]; ok {
			self.Type = NodeGateway
		}
		return nodeMap[selfNode]
	}
	gwMAC := neighLookupMAC(gwIP)
	id := gwMAC
	if id == "" {
		id = "gw-" + gwIP
	}
	node := &Node{
		ID:       id,
		MAC:      gwMAC,
		IPs:      []string{gwIP},
		Hostname: "",
		Type:     NodeGateway,
		Source:   "route",
	}
	nodeMap[id] = node
	return node
}

// ── WAN & Tunnel discovery ────────────────────────────────────────

func (s *Scanner) discoverWAN(nodeMap map[string]*Node, linkSet map[string]*Link, selfNode string) {
	// 1. Find the WAN gateway from ARP entries on non-LAN interfaces
	wanIDs := s.discoverWANGateway(nodeMap, linkSet, selfNode)

	// 2. Find WireGuard tunnels — link them upstream of WAN GW if present
	upstreamID := selfNode
	if len(wanIDs) > 0 {
		upstreamID = wanIDs[0] // link tunnel to the first WAN gateway
	}
	s.discoverWireGuard(nodeMap, linkSet, selfNode, upstreamID)
}

func (s *Scanner) discoverWANGateway(nodeMap map[string]*Node, linkSet map[string]*Link, selfNode string) []string {
	wanSet := make(map[string]bool)
	if s.wanIfaces != nil {
		for _, name := range s.wanIfaces() {
			wanSet[name] = true
		}
	}
	if len(wanSet) == 0 {
		return nil
	}

	// Build link-index → name map for matching
	ifaceNames := nlIfaceNames()
	wanIndices := make(map[int]bool)
	for idx, name := range ifaceNames {
		if wanSet[name] {
			wanIndices[idx] = true
		}
	}

	var wanIDs []string
	neighbors, err := vnl.NeighList(0, vnl.FAMILY_V4)
	if err != nil {
		return nil
	}

	for _, n := range neighbors {
		if !neighValid(n) || !wanIndices[n.LinkIndex] {
			continue
		}

		ip := n.IP.String()
		mac := strings.ToLower(n.HardwareAddr.String())
		iface := ifaceNames[n.LinkIndex]
		id := "wan-" + mac

		// discoverGateway() may have already created a node for this same
		// device — keyed by bare MAC — from the default route (e.g. an
		// ISP router that's both the default gateway and the ARP-visible
		// device on the WAN interface). Reuse that node instead of adding
		// a second one, otherwise the same physical device shows up twice
		// in the tree under two different IDs.
		if existing, ok := nodeMap[mac]; ok {
			id = mac
			existing.Type = NodeWANGW
			if existing.Hostname == "" {
				existing.Hostname = "WAN Gateway"
			}
			if existing.Iface == "" {
				existing.Iface = iface
			}
			if !slices.Contains(existing.IPs, ip) {
				existing.IPs = append(existing.IPs, ip)
			}
			if !strings.Contains(existing.Source, "arp") {
				existing.Source += ",arp"
			}
		} else {
			if _, exists := nodeMap[id]; exists {
				continue
			}
			nodeMap[id] = &Node{
				ID:       id,
				MAC:      mac,
				IPs:      []string{ip},
				Hostname: "WAN Gateway",
				Type:     NodeWANGW,
				Iface:    iface,
				Source:   "arp",
			}
		}

		linkKey := fmt.Sprintf("%s|%s", id, selfNode)
		if _, exists := linkSet[linkKey]; !exists {
			linkSet[linkKey] = &Link{
				SourceID: id,
				TargetID: selfNode,
				Type:     LinkWAN,
				Label:    iface,
			}
		}
		wanIDs = append(wanIDs, id)
	}

	// Handle point-to-point WAN interfaces (PPP, etc.) that have no ARP
	// neighbors.  Use the route peer address or the interface IP itself.
	if len(wanIDs) == 0 {
		wanIDs = s.discoverPPPWAN(nodeMap, linkSet, selfNode, wanSet)
	}

	return wanIDs
}

// discoverPPPWAN discovers WAN gateways on point-to-point interfaces (like
// ppp0) that have no ARP neighbors.  It looks at routes for a peer address
// or falls back to the remote endpoint IP from the interface itself.
func (s *Scanner) discoverPPPWAN(nodeMap map[string]*Node, linkSet map[string]*Link, selfNode string, wanSet map[string]bool) []string {
	var wanIDs []string

	// Check routes for WAN-interface-specific peer IPs
	routes, err := vnl.RouteList(nil, vnl.FAMILY_V4)
	if err != nil {
		return nil
	}
	ifaceNames := nlIfaceNames()

	for _, r := range routes {
		iface := ifaceNames[r.LinkIndex]
		if !wanSet[iface] {
			continue
		}
		// Point-to-point default route: either Dst==nil or Dst==0.0.0.0/0
		if r.Dst != nil {
			ones, bits := r.Dst.Mask.Size()
			if ones != 0 || bits == 0 {
				continue
			}
		}
		// For PPP: get the peer/remote IP from the interface addresses
		link, err := vnl.LinkByIndex(r.LinkIndex)
		if err != nil {
			continue
		}
		addrs, err := vnl.AddrList(link, vnl.FAMILY_V4)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if addr.Peer != nil && addr.Peer.IP != nil && !addr.Peer.IP.IsUnspecified() {
				peerIP := addr.Peer.IP.String()
				id := "wan-ppp-" + iface
				if _, exists := nodeMap[id]; exists {
					continue
				}
				nodeMap[id] = &Node{
					ID:       id,
					IPs:      []string{peerIP},
					Hostname: "WAN (" + iface + ")",
					Type:     NodeWANGW,
					Iface:    iface,
					Source:   "route",
				}
				linkKey := fmt.Sprintf("%s|%s", id, selfNode)
				linkSet[linkKey] = &Link{
					SourceID: id,
					TargetID: selfNode,
					Type:     LinkWAN,
					Label:    iface,
				}
				wanIDs = append(wanIDs, id)
			}
		}
		// If no peer address found, create a placeholder WAN node
		if len(wanIDs) == 0 {
			id := "wan-ppp-" + iface
			nodeMap[id] = &Node{
				ID:       id,
				Hostname: "WAN (" + iface + ")",
				Type:     NodeWANGW,
				Iface:    iface,
				Source:   "route",
			}
			linkKey := fmt.Sprintf("%s|%s", id, selfNode)
			linkSet[linkKey] = &Link{
				SourceID: id,
				TargetID: selfNode,
				Type:     LinkWAN,
				Label:    iface,
			}
			wanIDs = append(wanIDs, id)
		}
	}
	return wanIDs
}

func (s *Scanner) discoverWireGuard(nodeMap map[string]*Node, linkSet map[string]*Link, selfNode string, upstreamID string) {
	client, err := wgctrl.New()
	if err != nil {
		return // WireGuard not available
	}
	defer client.Close()

	devices, err := client.Devices()
	if err != nil {
		return
	}

	for _, dev := range devices {
		for _, peer := range dev.Peers {
			if peer.Endpoint == nil {
				continue
			}

			epHost := peer.Endpoint.IP.String()
			isFullTunnel := false
			for _, aip := range peer.AllowedIPs {
				ones, bits := aip.Mask.Size()
				if ones == 0 && bits > 0 {
					isFullTunnel = true
					break
				}
			}

			tunnelLabel := dev.Name
			if isFullTunnel {
				tunnelLabel = dev.Name + " (full tunnel)"
			}

			id := "tun-" + dev.Name
			ips := []string{epHost}

			for _, aip := range peer.AllowedIPs {
				ones, _ := aip.Mask.Size()
				if (aip.IP.To4() != nil && ones == 32) || (aip.IP.To4() == nil && ones == 128) {
					ipStr := aip.IP.String()
					if !slices.Contains(ips, ipStr) {
						ips = append(ips, ipStr)
					}
				}
			}

			hostname := "WireGuard " + dev.Name
			model := ""
			if peer.ReceiveBytes > 0 || peer.TransmitBytes > 0 {
				model = fmt.Sprintf("RX: %s, TX: %s",
					humanBytes64(peer.ReceiveBytes), humanBytes64(peer.TransmitBytes))
			}

			nodeMap[id] = &Node{
				ID:       id,
				MAC:      "",
				IPs:      ips,
				Hostname: hostname,
				Type:     NodeTunnel,
				Iface:    dev.Name,
				Model:    model,
				Source:   "wireguard",
			}

			linkKey := fmt.Sprintf("%s|%s", id, upstreamID)
			linkSet[linkKey] = &Link{
				SourceID: id,
				TargetID: upstreamID,
				Type:     LinkTunnel,
				Label:    tunnelLabel,
			}
		}
	}
}

func humanBytes64(b int64) string {
	switch {
	case b >= 1<<40:
		return fmt.Sprintf("%.1f TiB", float64(b)/float64(1<<40))
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// ── Netlink helpers ─────────────────────────────────────────────────────

// defaultGatewayIP returns the IPv4 default gateway using netlink routes.
func defaultGatewayIP() string {
	routes, err := vnl.RouteList(nil, vnl.FAMILY_V4)
	if err != nil {
		return ""
	}
	for _, r := range routes {
		if r.Dst == nil && r.Gw != nil {
			return r.Gw.String()
		}
	}
	for _, r := range routes {
		if r.Dst != nil && r.Gw != nil {
			ones, bits := r.Dst.Mask.Size()
			if ones == 0 && bits > 0 {
				return r.Gw.String()
			}
		}
	}
	return ""
}

// neighLookupMAC resolves an IP to its MAC via the kernel neighbor table.
func neighLookupMAC(ipStr string) string {
	target := net.ParseIP(ipStr)
	if target == nil {
		return ""
	}
	family := vnl.FAMILY_V4
	if target.To4() == nil {
		family = vnl.FAMILY_V6
	}
	neighbors, err := vnl.NeighList(0, family)
	if err != nil {
		return ""
	}
	for _, n := range neighbors {
		if n.IP.Equal(target) && len(n.HardwareAddr) > 0 {
			mac := n.HardwareAddr.String()
			if mac != "00:00:00:00:00:00" {
				return mac
			}
		}
	}
	return ""
}

// nlIfaceNames builds a map from link index to interface name.
func nlIfaceNames() map[int]string {
	links, err := vnl.LinkList()
	if err != nil {
		return nil
	}
	m := make(map[int]string, len(links))
	for _, l := range links {
		if a := l.Attrs(); a != nil {
			m[a.Index] = a.Name
		}
	}
	return m
}

// buildVLANParentMap returns a map from VLAN interface name to its parent
// physical interface name (e.g. "vlan3000" -> "enp3s0").
func buildVLANParentMap() map[string]string {
	links, err := vnl.LinkList()
	if err != nil {
		return nil
	}
	idxToName := make(map[int]string, len(links))
	for _, l := range links {
		if a := l.Attrs(); a != nil {
			idxToName[a.Index] = a.Name
		}
	}
	m := make(map[string]string)
	for _, l := range links {
		a := l.Attrs()
		if a == nil {
			continue
		}
		if a.ParentIndex > 0 {
			if parent, ok := idxToName[a.ParentIndex]; ok {
				m[a.Name] = parent
			}
		}
	}
	return m
}

// neighValid returns true if a neighbor entry has a resolved hardware address.
func neighValid(n vnl.Neigh) bool {
	if len(n.HardwareAddr) == 0 {
		return false
	}
	if n.HardwareAddr.String() == "00:00:00:00:00:00" {
		return false
	}
	// NUD_FAILED=0x20, NUD_INCOMPLETE=0x01, NUD_NONE=0x00
	if n.State == 0x20 || n.State == 0x00 || n.State == 0x01 {
		return false
	}
	return true
}

// neighStateStr converts a netlink NUD state bitmask to a readable string.
func neighStateStr(state int) string {
	switch {
	case state&0x80 != 0:
		return "PERMANENT"
	case state&0x02 != 0:
		return "REACHABLE"
	case state&0x04 != 0:
		return "STALE"
	case state&0x08 != 0:
		return "DELAY"
	case state&0x10 != 0:
		return "PROBE"
	default:
		return ""
	}
}

// ── Neighbor table (ARP/NDP via netlink) ──────────────────────────────────

func (s *Scanner) readNeighTable(nodeMap map[string]*Node, family int, sourceTag string) {
	ifaceNames := nlIfaceNames()
	neighbors, err := vnl.NeighList(0, family)
	if err != nil {
		log.Printf("topology: netlink NeighList %s: %v", sourceTag, err)
		return
	}

	for _, n := range neighbors {
		if !neighValid(n) {
			continue
		}
		ip := n.IP.String()
		if !netutil.IsLocalStr(ip, s.localNets) {
			continue
		}

		mac := strings.ToLower(n.HardwareAddr.String())
		// Skip if this MAC is already tracked as a WAN gateway
		if _, isWAN := nodeMap["wan-"+mac]; isWAN {
			continue
		}
		iface := ifaceNames[n.LinkIndex]
		state := neighStateStr(n.State)

		if existing, ok := nodeMap[mac]; ok {
			if !slices.Contains(existing.IPs, ip) {
				existing.IPs = append(existing.IPs, ip)
			}
			if existing.Iface == "" {
				existing.Iface = iface
			}
			if existing.State == "" {
				existing.State = state
			}
			if !strings.Contains(existing.Source, sourceTag) {
				existing.Source += "," + sourceTag
			}
		} else {
			nodeMap[mac] = &Node{
				ID:     mac,
				MAC:    mac,
				IPs:    []string{ip},
				Type:   NodeClient,
				Iface:  iface,
				State:  state,
				Source: sourceTag,
			}
		}
	}
}

// ── LLDP discovery ────────────────────────────────────────────────

var lldpChassisRe = regexp.MustCompile(`ChassisID:\s+mac\s+(\S+)`)
var lldpPortRe = regexp.MustCompile(`PortID:\s+(?:ifname|mac|local)\s+(\S+)`)
var lldpSysNameRe = regexp.MustCompile(`SysName:\s+(\S+)`)
var lldpSysDescRe = regexp.MustCompile(`SysDesc:\s+(.+)`)
var lldpMgmtIPRe = regexp.MustCompile(`MgmtIP:\s+(\S+)`)
var lldpPortDescRe = regexp.MustCompile(`PortDescr:\s+(.+)`)

func (s *Scanner) readLLDP(nodeMap map[string]*Node, linkSet map[string]*Link) {
	out, err := exec.Command("lldpctl", "-f", "keyvalue").Output()
	if err != nil {
		out, err = exec.Command("/usr/sbin/lldpctl", "-f", "keyvalue").Output()
	}
	if err != nil {
		out, err = exec.Command("lldpcli", "show", "neighbors", "details").Output()
		if err != nil {
			out, err = exec.Command("/usr/sbin/lldpcli", "show", "neighbors", "details").Output()
		}
		if err != nil {
			return
		}
		s.parseLLDPCLI(string(out), nodeMap, linkSet)
		return
	}
	s.parseLLDPCtlKeyValue(string(out), nodeMap, linkSet)
}

func (s *Scanner) parseLLDPCtlKeyValue(data string, nodeMap map[string]*Node, linkSet map[string]*Link) {
	type neighborInfo struct {
		localIface  string
		chassisMAC  string
		portID      string
		portDesc    string
		sysName     string
		sysDesc     string
		mgmtIP      string
		capBridge   bool
		capRouter   bool
		capWLAN     bool
	}

	neighbors := make(map[string]*neighborInfo)

	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := parts[0], parts[1]
		keyParts := strings.SplitN(key, ".", 3)
		if len(keyParts) < 3 || keyParts[0] != "lldp" {
			continue
		}
		iface := keyParts[1]
		rest := keyParts[2]

		ni, ok := neighbors[iface]
		if !ok {
			ni = &neighborInfo{localIface: iface}
			neighbors[iface] = ni
		}

		switch {
		case strings.HasSuffix(rest, "chassis.mac"):
			ni.chassisMAC = strings.ToLower(val)
		case strings.HasSuffix(rest, "port.ifname"), strings.HasSuffix(rest, "port.local"):
			ni.portID = val
		case strings.HasSuffix(rest, "port.descr"):
			ni.portDesc = val
		case strings.HasSuffix(rest, "chassis.name"):
			ni.sysName = val
		case strings.HasSuffix(rest, "chassis.descr"):
			ni.sysDesc = val
		case rest == "chassis.mgmt-ip":
			ni.mgmtIP = val
		case strings.HasSuffix(rest, "Bridge.enabled"):
			ni.capBridge = strings.ToLower(val) == "on"
		case strings.HasSuffix(rest, "Router.enabled"):
			ni.capRouter = strings.ToLower(val) == "on"
		case strings.HasSuffix(rest, "Wlan.enabled"):
			ni.capWLAN = strings.ToLower(val) == "on"
		}
	}

	for _, ni := range neighbors {
		if ni.chassisMAC == "" {
			continue
		}
		mac := ni.chassisMAC
		nodeType := classifyLLDPEx(ni.sysDesc, ni.capBridge, ni.capRouter, ni.capWLAN)

		if existing, ok := nodeMap[mac]; ok {
			existing.Type = promoteType(existing.Type, nodeType)
			if existing.Hostname == "" && ni.sysName != "" {
				existing.Hostname = ni.sysName
			}
			if ni.mgmtIP != "" && !slices.Contains(existing.IPs, ni.mgmtIP) {
				existing.IPs = append(existing.IPs, ni.mgmtIP)
			}
			if existing.Iface == "" {
				existing.Iface = ni.localIface
			}
			if !strings.Contains(existing.Source, "lldp") {
				existing.Source += ",lldp"
			}
		} else {
			ips := []string{}
			if ni.mgmtIP != "" {
				ips = []string{ni.mgmtIP}
			}
			nodeMap[mac] = &Node{
				ID:       mac,
				MAC:      mac,
				IPs:      ips,
				Hostname: ni.sysName,
				Type:     nodeType,
				Iface:    ni.localIface,
				Source:   "lldp",
			}
		}

		label := ni.portID
		if label == "" {
			label = ni.portDesc
		}
		linkKey := fmt.Sprintf("%s|%s", ni.localIface, mac)
		linkSet[linkKey] = &Link{
			SourceID: mac,
			TargetID: "self",
			Type:     LinkLLDP,
			Label:    label,
		}
	}
}

func (s *Scanner) parseLLDPCLI(data string, nodeMap map[string]*Node, linkSet map[string]*Link) {
	blocks := strings.Split(data, "Interface:")
	for _, block := range blocks[1:] {
		lines := strings.Split(block, "\n")
		if len(lines) == 0 {
			continue
		}
		localIface := strings.TrimSpace(strings.Split(lines[0], ",")[0])

		full := strings.Join(lines, "\n")
		var chassisMAC, sysName, sysDesc, mgmtIP, portID, portDesc string

		if m := lldpChassisRe.FindStringSubmatch(full); m != nil {
			chassisMAC = strings.ToLower(m[1])
		}
		if m := lldpPortRe.FindStringSubmatch(full); m != nil {
			portID = m[1]
		}
		if m := lldpSysNameRe.FindStringSubmatch(full); m != nil {
			sysName = strings.TrimSpace(m[1])
		}
		if m := lldpSysDescRe.FindStringSubmatch(full); m != nil {
			sysDesc = strings.TrimSpace(m[1])
		}
		if m := lldpMgmtIPRe.FindStringSubmatch(full); m != nil {
			mgmtIP = m[1]
		}
		if m := lldpPortDescRe.FindStringSubmatch(full); m != nil {
			portDesc = strings.TrimSpace(m[1])
		}

		if chassisMAC == "" {
			continue
		}

		nodeType := classifyLLDP(sysDesc)

		if existing, ok := nodeMap[chassisMAC]; ok {
			existing.Type = promoteType(existing.Type, nodeType)
			if existing.Hostname == "" && sysName != "" {
				existing.Hostname = sysName
			}
			if mgmtIP != "" && !slices.Contains(existing.IPs, mgmtIP) {
				existing.IPs = append(existing.IPs, mgmtIP)
			}
			if !strings.Contains(existing.Source, "lldp") {
				existing.Source += ",lldp"
			}
		} else {
			ips := []string{}
			if mgmtIP != "" {
				ips = []string{mgmtIP}
			}
			nodeMap[chassisMAC] = &Node{
				ID:       chassisMAC,
				MAC:      chassisMAC,
				IPs:      ips,
				Hostname: sysName,
				Type:     nodeType,
				Source:   "lldp",
			}
		}

		label := portID
		if label == "" {
			label = portDesc
		}
		linkKey := fmt.Sprintf("%s|%s", localIface, chassisMAC)
		linkSet[linkKey] = &Link{
			SourceID: chassisMAC,
			TargetID: "self",
			Type:     LinkLLDP,
			Label:    label,
		}
	}
}

func classifyLLDP(sysDesc string) NodeType {
	return classifyLLDPEx(sysDesc, false, false, false)
}

func classifyLLDPEx(sysDesc string, capBridge, capRouter, capWLAN bool) NodeType {
	lower := strings.ToLower(sysDesc)
	switch {
	case strings.Contains(lower, "router") || strings.Contains(lower, "gateway"):
		return NodeGateway
	case strings.Contains(lower, "switch") || strings.Contains(lower, "bridge"):
		return NodeSwitch
	case strings.Contains(lower, "access point") || strings.Contains(lower, "wireless"):
		return NodeAP
	}
	// Fall back to LLDP capabilities
	if capRouter {
		return NodeGateway
	}
	if capWLAN {
		return NodeAP
	}
	if capBridge {
		return NodeSwitch
	}
	return NodeClient
}

func promoteType(existing, candidate NodeType) NodeType {
	order := map[NodeType]int{
		NodeTunnel: 0, NodeWANGW: 1, NodeGateway: 2, NodeSwitch: 3, NodeAP: 4, NodeSelf: 5, NodeClient: 6,
	}
	if order[candidate] < order[existing] {
		return candidate
	}
	return existing
}

// ── WiFi controller integration ───────────────────────────────────

func (s *Scanner) mergeWiFiController(nodeMap map[string]*Node, linkSet map[string]*Link) {
	if s.wifiProvider == nil {
		return
	}
	summary := s.wifiProvider.GetSummary()
	if summary == nil {
		return
	}

	providerName := strings.ToLower(summary.ProviderName)

	// Build uplink map and device type index from controller data so we
	// can resolve uplink chains.  The controller sometimes reports an AP
	// as a switch's uplink when they share a cable (daisy-chain).  We walk
	// the chain to find the real infrastructure parent (switch or gateway).
	uplinkOf := make(map[string]string) // MAC -> uplink MAC
	deviceType := make(map[string]string) // MAC -> "ap"/"switch"
	for _, ap := range summary.APs {
		mac := strings.ToLower(ap.MAC)
		deviceType[mac] = "ap"
		if ul := strings.ToLower(ap.UplinkMAC); ul != "" {
			uplinkOf[mac] = ul
		}
	}
	for _, sw := range summary.Switches {
		mac := strings.ToLower(sw.MAC)
		deviceType[mac] = "switch"
		if ul := strings.ToLower(sw.UplinkMAC); ul != "" {
			uplinkOf[mac] = ul
		}
	}

	// resolveUplink walks the uplink chain to find the nearest switch,
	// gateway, or non-AP device.  Prevents routing switches through APs.
	resolveUplink := func(startMAC string) string {
		ul := uplinkOf[startMAC]
		seen := map[string]bool{startMAC: true}
		for i := 0; i < 10 && ul != ""; i++ {
			if seen[ul] {
				break // cycle
			}
			seen[ul] = true
			dt := deviceType[ul]
			if dt != "ap" {
				return ul // switch, gateway, or unknown (e.g. LLDP switch)
			}
			// Uplink is an AP — keep walking
			next := uplinkOf[ul]
			if next == "" {
				return ul // dead end, use the AP
			}
			ul = next
		}
		return ul
	}

	// Register APs
	for _, ap := range summary.APs {
		mac := strings.ToLower(ap.MAC)
		if mac == "" {
			continue
		}
		uplinkMAC := strings.ToLower(ap.UplinkMAC)
		if existing, ok := nodeMap[mac]; ok {
			existing.Type = NodeAP
			existing.Model = ap.Model
			if existing.Hostname == "" && ap.Name != "" {
				existing.Hostname = ap.Name
			}
			if ap.IP != "" && !slices.Contains(existing.IPs, ap.IP) {
				existing.IPs = append(existing.IPs, ap.IP)
			}
			if !strings.Contains(existing.Source, providerName) {
				existing.Source += "," + providerName
			}
		} else {
			ips := []string{}
			if ap.IP != "" {
				ips = []string{ap.IP}
			}
			nodeMap[mac] = &Node{
				ID:       mac,
				MAC:      mac,
				IPs:      ips,
				Hostname: ap.Name,
				Type:     NodeAP,
				Model:    ap.Model,
				Source:   providerName,
			}
		}
		// Link AP to its uplink device (switch or gateway)
		if uplinkMAC != "" {
			linkKey := fmt.Sprintf("%s|%s", uplinkMAC, mac)
			linkSet[linkKey] = &Link{
				SourceID: uplinkMAC,
				TargetID: mac,
				Type:     LinkWired,
			}
		}
	}

	// Register switches from WiFi controller
	for _, sw := range summary.Switches {
		mac := strings.ToLower(sw.MAC)
		if mac == "" {
			continue
		}
		uplinkMAC := strings.ToLower(resolveUplink(mac))
		if existing, ok := nodeMap[mac]; ok {
			existing.Type = promoteType(existing.Type, NodeSwitch)
			existing.Model = sw.Model
			if existing.Hostname == "" && sw.Name != "" {
				existing.Hostname = sw.Name
			}
			if sw.IP != "" && !slices.Contains(existing.IPs, sw.IP) {
				existing.IPs = append(existing.IPs, sw.IP)
			}
			if !strings.Contains(existing.Source, providerName) {
				existing.Source += "," + providerName
			}
		} else {
			ips := []string{}
			if sw.IP != "" {
				ips = []string{sw.IP}
			}
			nodeMap[mac] = &Node{
				ID:       mac,
				MAC:      mac,
				IPs:      ips,
				Hostname: sw.Name,
				Type:     NodeSwitch,
				Model:    sw.Model,
				Source:   providerName,
			}
		}
		// Link switch to its uplink device
		if uplinkMAC != "" {
			linkKey := fmt.Sprintf("%s|%s", uplinkMAC, mac)
			linkSet[linkKey] = &Link{
				SourceID: uplinkMAC,
				TargetID: mac,
				Type:     LinkWired,
			}
		}
	}

	// Register wireless clients and link them to their AP
	for _, cl := range summary.Clients {
		mac := strings.ToLower(cl.MAC)
		if mac == "" {
			continue
		}
		apMAC := strings.ToLower(cl.APMAC)

		if existing, ok := nodeMap[mac]; ok {
			if cl.IP != "" && !slices.Contains(existing.IPs, cl.IP) {
				existing.IPs = append(existing.IPs, cl.IP)
			}
			if existing.Hostname == "" && cl.Hostname != "" {
				existing.Hostname = cl.Hostname
			}
			existing.SSID = cl.SSID
			existing.Signal = cl.Signal
			existing.Radio = cl.Radio
			existing.APName = cl.APName
			if cl.DevCat != 0 {
				existing.DevCat = cl.DevCat
			}
			if !strings.Contains(existing.Source, providerName) {
				existing.Source += "," + providerName
			}
		} else {
			ips := []string{}
			if cl.IP != "" {
				ips = []string{cl.IP}
			}
			nodeMap[mac] = &Node{
				ID:       mac,
				MAC:      mac,
				IPs:      ips,
				Hostname: cl.Hostname,
				Type:     NodeClient,
				SSID:     cl.SSID,
				Signal:   cl.Signal,
				Radio:    cl.Radio,
				APName:   cl.APName,
				DevCat:   cl.DevCat,
				Source:   providerName,
			}
		}

		if apMAC != "" {
			linkKey := fmt.Sprintf("%s|%s", apMAC, mac)
			linkSet[linkKey] = &Link{
				SourceID: apMAC,
				TargetID: mac,
				Type:     LinkWireless,
				Label:    cl.SSID,
			}
		}
	}

	// Register wired clients with switch associations
	for _, cl := range summary.WiredClients {
		mac := strings.ToLower(cl.MAC)
		swMAC := strings.ToLower(cl.SwitchMAC)
		if mac == "" || swMAC == "" {
			continue
		}
		// Only create the link if both the client and the switch are known nodes
		if _, clientExists := nodeMap[mac]; !clientExists {
			continue
		}
		if _, switchExists := nodeMap[swMAC]; !switchExists {
			continue
		}
		if existing, ok := nodeMap[mac]; ok {
			if cl.IP != "" && !slices.Contains(existing.IPs, cl.IP) {
				existing.IPs = append(existing.IPs, cl.IP)
			}
			if existing.Hostname == "" && cl.Hostname != "" {
				existing.Hostname = cl.Hostname
			}
			if !strings.Contains(existing.Source, providerName) {
				existing.Source += "," + providerName
			}
		}
		// Create switch -> wired client link
		linkKey := fmt.Sprintf("%s|%s", swMAC, mac)
		linkSet[linkKey] = &Link{
			SourceID: swMAC,
			TargetID: mac,
			Type:     LinkWired,
		}
	}
}

// ── Hostname resolution ───────────────────────────────────────────

func (s *Scanner) resolveHostnames(nodeMap map[string]*Node) {
	if s.dns == nil {
		return
	}
	for _, node := range nodeMap {
		if node.Hostname != "" {
			continue
		}
		for _, ip := range node.IPs {
			parsed := net.ParseIP(ip)
			if parsed == nil || parsed.IsLinkLocalUnicast() {
				continue
			}
			if name := s.dns.LookupAddrAsync(ip); name != "" && name != ip {
				node.Hostname = name
				break
			}
		}
	}
}

// ── Device classification ─────────────────────────────────────────

// classifyDevices assigns a DeviceClass to client nodes based on hostname
// patterns, UniFi device category, and MAC OUI (manufacturer prefix).
func classifyDevices(nodeMap map[string]*Node) {
	for _, node := range nodeMap {
		if node.Type != NodeClient {
			continue
		}
		// 1. Try hostname-based classification first (most specific)
		if dc := classifyByHostname(node.Hostname); dc != "" {
			node.DeviceClass = dc
			continue
		}
		// 2. Try UniFi device category
		if dc := classifyByDevCat(node.DevCat); dc != "" {
			node.DeviceClass = dc
			continue
		}
		// 3. Fall back to vendor-based classification via MAC OUI
		if dc := classifyByOUI(node.MAC); dc != "" {
			node.DeviceClass = dc
		}
	}
}

// classifyByDevCat maps UniFi dev_cat IDs to device classes.
func classifyByDevCat(devCat int) string {
	switch devCat {
	case 1:
		return "computer"
	case 4:
		return "phone"
	case 6:
		return "gaming"
	case 7:
		return "media"
	case 13:
		return "camera"
	case 14:
		return "printer"
	case 15:
		return "iot"
	default:
		return ""
	}
}

// classifyByOUI classifies devices by their MAC address OUI prefix.
func classifyByOUI(mac string) string {
	if len(mac) < 8 {
		return ""
	}
	oui := strings.ToLower(mac[:8])

	// Camera manufacturers
	cameraOUIs := []string{
		"3c:64:cf", // Reolink
		"98:03:8e", // Reolink
		"98:ba:5f", // Reolink
		"ec:71:db", // Reolink
		"b4:6d:83", // Reolink
		"54:c4:15", // Reolink
		"28:29:86", // Reolink
		"c8:02:8f", // Hikvision
		"04:02:ca", // Hikvision
		"44:19:b6", // Hikvision
		"7c:09:b6", // Hikvision
		"c0:56:e3", // Hikvision
		"a4:cf:12", // Dahua
		"3c:ef:8c", // Dahua
		"40:f4:fd", // Dahua
		"e0:50:8b", // Dahua
		"ec:71:db", // Amcrest
		"9c:8e:cd", // Amcrest
		"00:40:8c", // Axis
		"ac:cc:8e", // Axis
		"b8:a4:4f", // Axis
	}
	for _, prefix := range cameraOUIs {
		if oui == prefix {
			return "camera"
		}
	}

	// IoT manufacturers
	iotOUIs := []string{
		"d8:f1:5b", // Espressif (ESP32/ESP8266)
		"24:6f:28", // Espressif
		"ac:67:b2", // Espressif
		"7c:df:a1", // Espressif
		"c4:4f:33", // Espressif
		"e8:68:e7", // Espressif
		"34:94:54", // Espressif
		"08:3a:f2", // Espressif
		"2c:f4:32", // Espressif
		"ec:fa:bc", // Espressif
		"e0:98:06", // Shelly
		"c8:2e:18", // Shelly
	}
	for _, prefix := range iotOUIs {
		if oui == prefix {
			return "iot"
		}
	}

	return ""
}

func classifyByHostname(hostname string) string {
	h := strings.ToLower(hostname)
	if h == "" {
		return ""
	}

	// Cameras
	for _, p := range []string{"cam-", "cam.", "camera", "ipcam", "hikvision", "reolink", "dahua", "amcrest", "axis-", "flir-"} {
		if strings.Contains(h, p) {
			return "camera"
		}
	}

	// IoT / Smart Home
	for _, p := range []string{"tasmota", "shelly", "esp32-", "esp-", "esp8266", "eve.", "hue-", "ikea-", "zigbee", "zwave", "sonoff", "tuya", "meross", "wled-", "mqtt-"} {
		if strings.Contains(h, p) {
			return "iot"
		}
	}

	// Voice assistants
	for _, p := range []string{"home-assistant-voice", "echo-", "alexa", "google-home", "homepod", "nest-hub"} {
		if strings.Contains(h, p) {
			return "voice"
		}
	}

	// Phones / Tablets
	for _, p := range []string{"iphone", "ipad", "pixel-", "galaxy-", "android-", "oneplus", "huawei-", "xiaomi-"} {
		if strings.Contains(h, p) {
			return "phone"
		}
	}

	// Computers
	for _, p := range []string{"macbook", "imac", "mac-mini", "macpro", "desktop-", "laptop-", "thinkpad", "surface-", "xps-"} {
		if strings.Contains(h, p) {
			return "computer"
		}
	}

	// Media / Entertainment
	for _, p := range []string{"teufel", "sonos", "chromecast", "appletv", "apple-tv", "fire-tv", "firetv", "roku", "shield", "plex", "kodi"} {
		if strings.Contains(h, p) {
			return "media"
		}
	}

	// NAS / Servers
	for _, p := range []string{"diskstation", "synology", "nas-", "nas.", "qnap", "proxmox", "truenas", "freenas", "unraid"} {
		if strings.Contains(h, p) {
			return "server"
		}
	}

	// Printers
	for _, p := range []string{"printer", "epson", "canon-", "brother-", "hp-print", "laserjet", "officejet"} {
		if strings.Contains(h, p) {
			return "printer"
		}
	}

	// Gaming
	for _, p := range []string{"playstation", "ps5-", "ps4-", "xbox", "nintendo", "switch-", "steamdeck"} {
		if strings.Contains(h, p) {
			return "gaming"
		}
	}

	return ""
}

// ── Link inference ────────────────────────────────────────────────

func (s *Scanner) inferLinks(nodeMap map[string]*Node, linkSet map[string]*Link, selfID string, gwNode *Node) {
	// Resolve placeholder "self" targets to the actual self MAC.
	for key, link := range linkSet {
		if link.TargetID == "self" {
			link.TargetID = selfID
		}
		if link.SourceID == link.TargetID {
			delete(linkSet, key)
		}
	}

	// Link self to gateway (gateway is the upstream parent of self).
	if gwNode != nil && selfID != "" && selfID != gwNode.ID {
		linkKey := fmt.Sprintf("%s|%s", gwNode.ID, selfID)
		if _, exists := linkSet[linkKey]; !exists {
			linkSet[linkKey] = &Link{
				SourceID: gwNode.ID,
				TargetID: selfID,
				Type:     LinkWired,
			}
		}
	}

	// hasUpstream tracks which nodes are a TARGET of some link, meaning
	// they already have an upstream parent in the tree.
	hasUpstream := make(map[string]bool)
	for _, link := range linkSet {
		hasUpstream[link.TargetID] = true
	}

	// Collect switches and build an interface-to-switch index so we can
	// route wired clients through the correct switch.  If multiple switches
	// share the same interface, mark it ambiguous to avoid random assignment.
	var switches []string
	switchByIface := make(map[string]string)
	ambiguousIface := make(map[string]bool)
	for mac, node := range nodeMap {
		if node.Type == NodeSwitch {
			switches = append(switches, mac)
			if node.Iface != "" {
				if _, exists := switchByIface[node.Iface]; exists {
					ambiguousIface[node.Iface] = true
				}
				switchByIface[node.Iface] = mac
			}
		}
	}
	for iface := range ambiguousIface {
		delete(switchByIface, iface)
	}

	// Build a VLAN-to-parent-interface map so devices on VLAN interfaces
	// can be routed through the switch on the parent physical interface.
	vlanParent := buildVLANParentMap()

	// lookupSwitch resolves an interface name to its switch, checking the
	// direct interface first, then falling back to its VLAN parent.
	lookupSwitch := func(iface string) (string, bool) {
		if swMAC, ok := switchByIface[iface]; ok {
			return swMAC, true
		}
		if parent, ok := vlanParent[iface]; ok {
			if swMAC, ok := switchByIface[parent]; ok {
				return swMAC, true
			}
		}
		return "", false
	}

	// Ensure every switch has an upstream link.  If there's an LLDP-
	// discovered switch on the gateway's local interface, route other
	// switches through it (it's the physical uplink switch).
	var uplinkSwitch string
	if gwNode != nil {
		// Find the self node's primary interface
		selfIface := ""
		if self, ok := nodeMap[selfID]; ok {
			selfIface = self.Iface
		}
		// Find LLDP switches on the same interface as the gateway/self
		for _, swMAC := range switches {
			sw := nodeMap[swMAC]
			if sw == nil {
				continue
			}
			isLLDP := strings.Contains(sw.Source, "lldp")
			if isLLDP && sw.Iface != "" && (sw.Iface == selfIface || selfIface == "") {
				uplinkSwitch = swMAC
				break
			}
		}
	}

	for _, swMAC := range switches {
		if hasUpstream[swMAC] {
			continue
		}
		// Route through the uplink switch if this is a different switch
		if uplinkSwitch != "" && swMAC != uplinkSwitch {
			linkKey := fmt.Sprintf("%s|%s", uplinkSwitch, swMAC)
			linkSet[linkKey] = &Link{
				SourceID: uplinkSwitch,
				TargetID: swMAC,
				Type:     LinkWired,
			}
			hasUpstream[swMAC] = true
			continue
		}
		// Otherwise this switch uplinks directly into self — self is the
		// actual LAN router/hub these devices are physically attached to.
		// Only fall back to the WAN gateway if we have no self node at all
		// (e.g. self couldn't be discovered), since the gateway is the
		// upstream-of-self WAN device, not a stand-in for the LAN router.
		target := selfID
		if target == "" && gwNode != nil {
			target = gwNode.ID
		}
		if target == "" {
			continue
		}
		linkKey := fmt.Sprintf("%s|%s", target, swMAC)
		linkSet[linkKey] = &Link{
			SourceID: target,
			TargetID: swMAC,
			Type:     LinkWired,
		}
		hasUpstream[swMAC] = true
	}

	// Ensure every AP has an upstream link.  APs may already be linked
	// as sources of wireless client links, but they still need a parent
	// connection (to a switch on the same interface, to self — the LAN
	// router these devices are physically attached to — or, failing
	// that, to the WAN gateway).
	for mac, node := range nodeMap {
		if node.Type != NodeAP {
			continue
		}
		if hasUpstream[mac] {
			continue
		}
		if mac == selfID || (gwNode != nil && mac == gwNode.ID) {
			continue
		}
		target := ""
		if node.Iface != "" {
			if swMAC, ok := lookupSwitch(node.Iface); ok {
				target = swMAC
			}
		}
		if target == "" && selfID != "" {
			target = selfID
		}
		if target == "" && gwNode != nil {
			target = gwNode.ID
		}
		if target != "" {
			linkKey := fmt.Sprintf("%s|%s", target, mac)
			linkSet[linkKey] = &Link{
				SourceID: target,
				TargetID: mac,
				Type:     LinkWired,
			}
			hasUpstream[mac] = true
		}
	}

	// Rebuild the full linked set (nodes that appear in any link at all)
	// so we can find truly orphaned nodes.
	linked := make(map[string]bool)
	for _, link := range linkSet {
		linked[link.SourceID] = true
		linked[link.TargetID] = true
	}

	// Connect remaining orphan nodes: prefer routing wired clients
	// through a switch, then through self (the LAN router these devices
	// are physically attached to), and only fall back to the WAN
	// gateway if self is unknown.
	for mac, node := range nodeMap {
		if linked[mac] {
			continue
		}
		if mac == selfID || (gwNode != nil && mac == gwNode.ID) {
			continue
		}
		target := ""
		// Wired clients (no SSID) on a known switch interface
		if node.SSID == "" && node.Iface != "" {
			if swMAC, ok := lookupSwitch(node.Iface); ok {
				target = swMAC
			}
		}
		if target == "" && selfID != "" {
			target = selfID
		}
		if target == "" && gwNode != nil {
			target = gwNode.ID
		}
		if target != "" {
			linkKey := fmt.Sprintf("%s|%s", target, mac)
			linkSet[linkKey] = &Link{
				SourceID: target,
				TargetID: mac,
				Type:     LinkWired,
			}
		}
	}
}
