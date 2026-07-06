package debug

import (
	"context"
	"fmt"
	"log"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"bandwidth-monitor/netutil"
	"bandwidth-monitor/resolver"

	mdns "github.com/miekg/dns"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// sanitizeError removes file paths and internal details from error messages
// to avoid leaking system information to the frontend.
func sanitizeError(err error) string {
	s := err.Error()
	// Strip file paths
	for _, prefix := range []string{"/etc/", "/var/", "/tmp/", "/run/", "/home/", "/root/"} {
		for {
			idx := strings.Index(s, prefix)
			if idx < 0 {
				break
			}
			// Find end of path (next space or colon or end)
			end := idx
			for end < len(s) && s[end] != ' ' && s[end] != ':' && s[end] != ')' {
				end++
			}
			s = s[:idx] + "[path]" + s[end:]
		}
	}
	return s
}

// ── Traceroute (native Go, no external binary) ──

// icmpConn is a thin wrapper around a raw ICMP net.PacketConn, providing
// the ipv4.PacketConn/ipv6.PacketConn accessors that golang.org/x/net/icmp's
// PacketConn also offers. We build our own instead of using icmp.ListenPacket
// directly because that package's ListenPacket doesn't expose any way to
// bind the resulting socket to a specific network interface — which we need
// for per-WAN-interface traceroute/MTU tests. All other methods (ReadFrom,
// WriteTo, SetDeadline, Close, ...) are provided by the embedded
// net.PacketConn.
type icmpConn struct {
	net.PacketConn
	p4 *ipv4.PacketConn
	p6 *ipv6.PacketConn
}

func (c *icmpConn) IPv4PacketConn() *ipv4.PacketConn { return c.p4 }
func (c *icmpConn) IPv6PacketConn() *ipv6.PacketConn { return c.p6 }

// listenICMP opens a raw ICMP (v4 or v6) socket, optionally bound to a
// specific WAN interface via SO_BINDTODEVICE (pass "" for the default
// route/no binding). See netutil.DialerForInterface for the rationale and
// caveats around interface binding.
func listenICMP(isV4 bool, iface string) (*icmpConn, error) {
	network, address := "ip4:icmp", "0.0.0.0"
	if !isV4 {
		network, address = "ip6:ipv6-icmp", "::"
	}
	pc, err := listenRawIP(network, address, iface)
	if err != nil {
		return nil, err
	}
	conn := &icmpConn{PacketConn: pc}
	if isV4 {
		conn.p4 = ipv4.NewPacketConn(pc)
	} else {
		conn.p6 = ipv6.NewPacketConn(pc)
	}
	return conn, nil
}

// listenRawIP opens a raw IP-level packet connection (e.g. "ip4:icmp"),
// optionally bound to a specific WAN interface via SO_BINDTODEVICE.
func listenRawIP(network, address, iface string) (net.PacketConn, error) {
	return netutil.ListenConfigForInterface(iface).ListenPacket(context.Background(), network, address)
}

// TracerouteHop holds aggregated stats for one TTL hop.
type TracerouteHop struct {
	TTL      int     `json:"ttl"`
	IP       string  `json:"ip"`
	Hostname string  `json:"hostname,omitempty"`
	AvgRTT   float64 `json:"avg_rtt_ms"`
	MinRTT   float64 `json:"min_rtt_ms"`
	MaxRTT   float64 `json:"max_rtt_ms"`
	LossPct  float64 `json:"loss_pct"`
	Sent     int     `json:"sent"`
	Received int     `json:"received"`
}

// TracerouteResult is the complete result returned to the frontend.
type TracerouteResult struct {
	Target       string          `json:"target"`
	ResolvedIP   string          `json:"resolved_ip"`
	MaxTTL       int             `json:"max_ttl"`
	ProbesPerTTL int             `json:"probes_per_ttl"`
	Hops         []TracerouteHop `json:"hops"`
	ReachedDest  bool            `json:"reached_dest"`
	Error        string          `json:"error,omitempty"`
	Timestamp    int64           `json:"timestamp"`
}

// TracerouteProgress is sent via SSE while running.
type TracerouteProgress struct {
	Phase   string            `json:"phase"` // "running", "done", "error"
	Message string            `json:"message"`
	TTL     int               `json:"ttl,omitempty"`
	Result  *TracerouteResult `json:"result,omitempty"`
}

// RunTraceroute performs N probes per hop from TTL 1..maxTTL using ICMP echo.
// It streams progress updates over the returned channel.
func RunTraceroute(target string, probesPerTTL int, maxTTL int, dns *resolver.Resolver, iface string) <-chan TracerouteProgress {
	ch := make(chan TracerouteProgress, 64)

	go func() {
		defer close(ch)

		// Resolve target
		ips, err := net.LookupIP(target)
		if err != nil {
			ch <- TracerouteProgress{Phase: "error", Message: fmt.Sprintf("cannot resolve %s", target)}
			return
		}

		// Prefer IPv4
		var destIP net.IP
		for _, ip := range ips {
			if ip.To4() != nil {
				destIP = ip.To4()
				break
			}
		}
		if destIP == nil && len(ips) > 0 {
			destIP = ips[0] // fallback to IPv6
		}
		if destIP == nil {
			ch <- TracerouteProgress{Phase: "error", Message: fmt.Sprintf("no IP found for %s", target)}
			return
		}

		isV4 := destIP.To4() != nil
		if iface != "" {
			log.Printf("debug/traceroute: starting to %s (%s) via interface %s, %d probes/ttl, max TTL %d", target, destIP, iface, probesPerTTL, maxTTL)
		} else {
			log.Printf("debug/traceroute: starting to %s (%s), %d probes/ttl, max TTL %d", target, destIP, probesPerTTL, maxTTL)
		}
		ch <- TracerouteProgress{Phase: "running", Message: fmt.Sprintf("Traceroute to %s (%s) — %d probes per hop", target, destIP, probesPerTTL)}

		var hops []TracerouteHop
		reachedDest := false

		// Open a single ICMP socket for the entire traceroute run.
		conn, err := listenICMP(isV4, iface)
		if err != nil {
			msg := fmt.Sprintf("cannot open ICMP socket (need root/CAP_NET_RAW): %v", err)
			if iface != "" {
				msg = fmt.Sprintf("cannot open ICMP socket bound to interface %s (need root/CAP_NET_RAW, and the interface needs its own working route): %v", iface, err)
			}
			ch <- TracerouteProgress{Phase: "error", Message: msg}
			return
		}
		defer conn.Close()

		for ttl := 1; ttl <= maxTTL; ttl++ {
			hop := probeHop(conn, destIP, ttl, probesPerTTL, isV4, dns)
			hops = append(hops, hop)

			ch <- TracerouteProgress{
				Phase:   "running",
				Message: fmt.Sprintf("TTL %d: %s (%.1f ms, %.0f%% loss)", ttl, hopLabel(hop), hop.AvgRTT, hop.LossPct),
				TTL:     ttl,
			}

			if hop.IP != "" && hop.IP == destIP.String() {
				reachedDest = true
				break
			}
		}

		result := &TracerouteResult{
			Target:       target,
			ResolvedIP:   destIP.String(),
			MaxTTL:       maxTTL,
			ProbesPerTTL: probesPerTTL,
			Hops:         hops,
			ReachedDest:  reachedDest,
			Timestamp:    time.Now().UnixMilli(),
		}

		ch <- TracerouteProgress{Phase: "done", Message: fmt.Sprintf("Complete — %d hops", len(hops)), Result: result}
		log.Printf("debug/traceroute: finished %s — %d hops, reached=%v", target, len(hops), reachedDest)
	}()

	return ch
}

func hopLabel(h TracerouteHop) string {
	if h.IP == "" {
		return "*"
	}
	if h.Hostname != "" {
		return h.Hostname + " (" + h.IP + ")"
	}
	return h.IP
}

func probeHop(conn *icmpConn, dest net.IP, ttl int, count int, isV4 bool, dns *resolver.Resolver) TracerouteHop {
	hop := TracerouteHop{TTL: ttl, Sent: count}

	var rtts []float64
	respondentIP := ""

	for i := 0; i < count; i++ {
		ip, rtt, err := sendProbe(conn, dest, ttl, isV4, i)
		if err != nil {
			continue
		}
		rtts = append(rtts, rtt)
		if ip != "" {
			respondentIP = ip
		}
		// Small delay between probes to avoid flooding
		time.Sleep(10 * time.Millisecond)
	}

	hop.Received = len(rtts)
	hop.LossPct = float64(count-len(rtts)) / float64(count) * 100

	if len(rtts) > 0 {
		hop.IP = respondentIP
		var sum float64
		hop.MinRTT = math.MaxFloat64
		for _, r := range rtts {
			sum += r
			if r < hop.MinRTT {
				hop.MinRTT = r
			}
			if r > hop.MaxRTT {
				hop.MaxRTT = r
			}
		}
		hop.AvgRTT = sum / float64(len(rtts))

		// Reverse DNS via shared resolver (always fresh for debug diagnostics).
		if hop.IP != "" && dns != nil {
			hop.Hostname = dns.LookupAddrFresh(hop.IP)
		}
	} else {
		hop.MinRTT = 0
	}

	return hop
}

func sendProbe(conn *icmpConn, dest net.IP, ttl int, isV4 bool, seq int) (respIP string, rttMs float64, err error) {
	if isV4 {
		return sendProbeV4(conn, dest, ttl, seq)
	}
	return sendProbeV6(conn, dest, ttl, seq)
}

func sendProbeV4(conn *icmpConn, dest net.IP, ttl int, seq int) (string, float64, error) {
	raw := conn.IPv4PacketConn()
	if err := raw.SetTTL(ttl); err != nil {
		return "", 0, fmt.Errorf("set TTL: %w", err)
	}
	raw.SetControlMessage(ipv4.FlagTTL, true)
	conn.SetDeadline(time.Now().Add(1 * time.Second))

	// Encode TTL and seq in the ICMP ID/Seq so we can match responses
	id := uint16(ttl)<<8 | uint16(seq&0xFF)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(id),
			Seq:  seq,
			Data: []byte("bwmon-tr"),
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return "", 0, err
	}

	dst := &net.IPAddr{IP: dest}
	start := time.Now()
	if _, err := conn.WriteTo(wb, dst); err != nil {
		return "", 0, err
	}

	// Read responses — we may get other ICMP traffic, so loop briefly
	rb := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			return "", 0, err // timeout
		}
		rtt := float64(time.Since(start).Microseconds()) / 1000.0

		rm, err := icmp.ParseMessage(1, rb[:n])
		if err != nil {
			continue
		}

		peerIP := ""
		if addr, ok := peer.(*net.IPAddr); ok {
			peerIP = addr.IP.String()
		}

		switch rm.Type {
		case ipv4.ICMPTypeEchoReply:
			// Check if this is our echo reply
			if echo, ok := rm.Body.(*icmp.Echo); ok {
				if uint16(echo.ID) == id {
					return peerIP, rtt, nil
				}
			}
			continue // not our reply
		case ipv4.ICMPTypeTimeExceeded:
			// Time Exceeded contains the original IP header + first 8 bytes of original ICMP
			// The inner ICMP echo ID should match ours
			if body, ok := rm.Body.(*icmp.TimeExceeded); ok {
				if matchInnerICMP(body.Data, id) {
					return peerIP, rtt, nil
				}
			}
			// Some systems return raw bytes — try to match anyway
			return peerIP, rtt, nil
		case ipv4.ICMPTypeDestinationUnreachable:
			return peerIP, rtt, nil
		}
	}
}

// matchInnerICMP checks if the inner ICMP echo (embedded in Time Exceeded)
// matches our probe ID. The data contains the original IP header + 8 bytes.
func matchInnerICMP(data []byte, expectedID uint16) bool {
	if len(data) < 28 {
		return false // need at least 20 (IP hdr) + 8 (ICMP echo hdr)
	}
	// IP header length is in the first nibble
	ihl := int(data[0]&0x0F) * 4
	if ihl < 20 || len(data) < ihl+4 {
		return false
	}
	// ICMP echo: type(1) + code(1) + checksum(2) + id(2)
	innerID := uint16(data[ihl+4])<<8 | uint16(data[ihl+5])
	return innerID == expectedID
}

func sendProbeV6(conn *icmpConn, dest net.IP, ttl int, seq int) (string, float64, error) {
	raw := conn.IPv6PacketConn()
	if err := raw.SetHopLimit(ttl); err != nil {
		return "", 0, fmt.Errorf("set hop limit: %w", err)
	}
	conn.SetDeadline(time.Now().Add(1 * time.Second))

	id := uint16(ttl)<<8 | uint16(seq&0xFF)
	msg := icmp.Message{
		Type: ipv6.ICMPTypeEchoRequest,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(id),
			Seq:  seq,
			Data: []byte("bwmon-tr"),
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return "", 0, err
	}

	dst := &net.IPAddr{IP: dest}
	start := time.Now()
	if _, err := conn.WriteTo(wb, dst); err != nil {
		return "", 0, err
	}

	rb := make([]byte, 1500)
	for {
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			return "", 0, err
		}
		rtt := float64(time.Since(start).Microseconds()) / 1000.0

		rm, err := icmp.ParseMessage(58, rb[:n])
		if err != nil {
			continue
		}

		peerIP := ""
		if addr, ok := peer.(*net.IPAddr); ok {
			peerIP = addr.IP.String()
		}

		switch rm.Type {
		case ipv6.ICMPTypeEchoReply:
			if echo, ok := rm.Body.(*icmp.Echo); ok {
				if uint16(echo.ID) == id {
					return peerIP, rtt, nil
				}
			}
			continue
		case ipv6.ICMPTypeTimeExceeded:
			// For IPv6, the inner packet starts immediately (no variable IP header)
			return peerIP, rtt, nil
		case ipv6.ICMPTypeDestinationUnreachable:
			return peerIP, rtt, nil
		}
	}
}

// ── Path MTU Discovery ──

// MTUProbeResult holds the result of a single MTU probe.
type MTUProbeResult struct {
	Size    int     `json:"size"`
	Success bool    `json:"success"`
	RTT     float64 `json:"rtt_ms"`
	Error   string  `json:"error,omitempty"`
}

// MTUResult is the complete result returned to the frontend.
type MTUResult struct {
	Target     string           `json:"target"`
	ResolvedIP string           `json:"resolved_ip"`
	PathMTU    int              `json:"path_mtu"`
	LocalMTU   int              `json:"local_mtu,omitempty"`
	Probes     []MTUProbeResult `json:"probes"`
	Timestamp  int64            `json:"timestamp"`
}

// MTUProgress is sent via SSE while running.
type MTUProgress struct {
	Phase   string     `json:"phase"` // "running", "done", "error"
	Message string     `json:"message"`
	Size    int        `json:"size,omitempty"`
	Result  *MTUResult `json:"result,omitempty"`
}

// RunMTUDiscovery performs a binary-search path MTU discovery by sending
// ICMP echo packets with the DF (Don't Fragment) bit set. It streams
// progress updates over the returned channel.
func RunMTUDiscovery(target string, iface string) <-chan MTUProgress {
	ch := make(chan MTUProgress, 64)

	go func() {
		defer close(ch)

		// Resolve target
		ips, err := net.LookupIP(target)
		if err != nil {
			ch <- MTUProgress{Phase: "error", Message: fmt.Sprintf("cannot resolve %s", target)}
			return
		}

		var destIP net.IP
		for _, ip := range ips {
			if ip.To4() != nil {
				destIP = ip.To4()
				break
			}
		}
		if destIP == nil {
			ch <- MTUProgress{Phase: "error", Message: "MTU discovery requires an IPv4 target"}
			return
		}

		if iface != "" {
			log.Printf("debug/mtu: starting path MTU discovery to %s (%s) via interface %s", target, destIP, iface)
			ch <- MTUProgress{Phase: "running", Message: fmt.Sprintf("Path MTU discovery to %s (%s) via %s", target, destIP, iface)}
		} else {
			log.Printf("debug/mtu: starting path MTU discovery to %s (%s)", target, destIP)
			ch <- MTUProgress{Phase: "running", Message: fmt.Sprintf("Path MTU discovery to %s (%s)", target, destIP)}
		}

		// Detect the local interface MTU for the route to destIP
		localMTU := detectLocalMTU(destIP, iface)

		// Open a raw ICMP socket, optionally bound to a specific WAN
		// interface, so we can access the underlying fd to set the Don't
		// Fragment socket option.
		rawPC, err := listenRawIP("ip4:icmp", "0.0.0.0", iface)
		if err != nil {
			msg := fmt.Sprintf("cannot open ICMP socket (need root/CAP_NET_RAW): %v", err)
			if iface != "" {
				msg = fmt.Sprintf("cannot open ICMP socket bound to interface %s (need root/CAP_NET_RAW, and the interface needs its own working route): %v", iface, err)
			}
			ch <- MTUProgress{Phase: "error", Message: msg}
			return
		}
		defer rawPC.Close()

		// Set the Don't Fragment bit on the socket
		setDontFragment(rawPC)

		// Binary search for the path MTU between 68 (IPv4 minimum) and 1500
		// (standard Ethernet). Upper bound may be raised if local MTU is higher.
		upper := 1500
		if localMTU > 1500 {
			upper = localMTU
		}
		lower := 68
		var probes []MTUProbeResult

		// First, check if the upper bound works — skip binary search if so
		ok, rtt, probeErr := probeMTU(rawPC, destIP, upper)
		probes = append(probes, MTUProbeResult{Size: upper, Success: ok, RTT: rtt, Error: probeErr})
		ch <- MTUProgress{Phase: "running", Message: fmt.Sprintf("Testing %d bytes: %s", upper, mtuStatus(ok)), Size: upper}

		if ok {
			// Full size works — path MTU is at least the upper bound
			result := &MTUResult{
				Target:     target,
				ResolvedIP: destIP.String(),
				PathMTU:    upper,
				LocalMTU:   localMTU,
				Probes:     probes,
				Timestamp:  time.Now().UnixMilli(),
			}
			ch <- MTUProgress{Phase: "done", Message: fmt.Sprintf("Path MTU: %d bytes (full size passes)", upper), Result: result}
			log.Printf("debug/mtu: finished %s — path MTU %d", target, upper)
			return
		}

		// Also verify the minimum works
		ok, rtt, probeErr = probeMTU(rawPC, destIP, lower)
		probes = append(probes, MTUProbeResult{Size: lower, Success: ok, RTT: rtt, Error: probeErr})
		ch <- MTUProgress{Phase: "running", Message: fmt.Sprintf("Testing %d bytes: %s", lower, mtuStatus(ok)), Size: lower}

		if !ok {
			result := &MTUResult{
				Target:     target,
				ResolvedIP: destIP.String(),
				PathMTU:    0,
				LocalMTU:   localMTU,
				Probes:     probes,
				Timestamp:  time.Now().UnixMilli(),
			}
			ch <- MTUProgress{Phase: "done", Message: "Host unreachable or blocks ICMP — cannot determine MTU", Result: result}
			log.Printf("debug/mtu: finished %s — host unreachable", target)
			return
		}

		// Binary search
		for upper-lower > 1 {
			mid := (lower + upper) / 2
			ok, rtt, probeErr = probeMTU(rawPC, destIP, mid)
			probes = append(probes, MTUProbeResult{Size: mid, Success: ok, RTT: rtt, Error: probeErr})
			ch <- MTUProgress{Phase: "running", Message: fmt.Sprintf("Testing %d bytes: %s (range %d–%d)", mid, mtuStatus(ok), lower, upper), Size: mid}

			if ok {
				lower = mid
			} else {
				upper = mid
			}
		}

		// Sort probes by size for display
		sortProbes(probes)

		result := &MTUResult{
			Target:     target,
			ResolvedIP: destIP.String(),
			PathMTU:    lower,
			LocalMTU:   localMTU,
			Probes:     probes,
			Timestamp:  time.Now().UnixMilli(),
		}
		ch <- MTUProgress{Phase: "done", Message: fmt.Sprintf("Path MTU: %d bytes", lower), Result: result}
		log.Printf("debug/mtu: finished %s — path MTU %d, local MTU %d", target, lower, localMTU)
	}()

	return ch
}

func mtuStatus(ok bool) string {
	if ok {
		return "pass"
	}
	return "blocked (DF bit set, packet too large)"
}

func sortProbes(probes []MTUProbeResult) {
	for i := 1; i < len(probes); i++ {
		for j := i; j > 0 && probes[j].Size < probes[j-1].Size; j-- {
			probes[j], probes[j-1] = probes[j-1], probes[j]
		}
	}
}

// probeMTU sends an ICMP echo with DF bit at the given packet size and returns
// whether it succeeded. Size is total IP packet size (header + payload).
func probeMTU(pc net.PacketConn, dest net.IP, size int) (bool, float64, string) {
	// IP header is 20 bytes, ICMP header is 8 bytes
	payloadSize := size - 20 - 8
	if payloadSize < 0 {
		payloadSize = 0
	}

	pc.SetDeadline(time.Now().Add(2 * time.Second))

	payload := make([]byte, payloadSize)
	for i := range payload {
		payload[i] = 'M'
	}
	id := uint16(size & 0xFFFF)
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEcho,
		Code: 0,
		Body: &icmp.Echo{
			ID:   int(id),
			Seq:  size,
			Data: payload,
		},
	}
	wb, err := msg.Marshal(nil)
	if err != nil {
		return false, 0, sanitizeError(err)
	}

	dst := &net.IPAddr{IP: dest}
	start := time.Now()
	if _, err := pc.WriteTo(wb, dst); err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "message too long") {
			return false, 0, "packet too large for local interface"
		}
		return false, 0, sanitizeError(err)
	}

	// Wait for reply — retry a couple of times in case of spurious ICMP
	rb := make([]byte, 1500)
	for attempts := 0; attempts < 3; attempts++ {
		n, _, err := pc.ReadFrom(rb)
		if err != nil {
			return false, 0, "timeout"
		}
		rtt := float64(time.Since(start).Microseconds()) / 1000.0
		rm, err := icmp.ParseMessage(1, rb[:n])
		if err != nil {
			continue
		}

		switch rm.Type {
		case ipv4.ICMPTypeEchoReply:
			if echo, ok := rm.Body.(*icmp.Echo); ok {
				if uint16(echo.ID) == id {
					return true, rtt, ""
				}
			}
			continue
		case ipv4.ICMPTypeDestinationUnreachable:
			// Code 4 = fragmentation needed but DF set
			if rm.Code == 4 {
				return false, rtt, "fragmentation needed (DF set)"
			}
			return false, rtt, fmt.Sprintf("destination unreachable (code %d)", rm.Code)
		}
	}
	return false, 0, "timeout"
}

// detectLocalMTU finds the MTU of the interface that routes to the given IP.
// If iface is non-empty, it looks up that specific interface's MTU directly
// instead of using the subnet-matching heuristic.
func detectLocalMTU(dest net.IP, iface string) int {
	ifaces, err := net.Interfaces()
	if err != nil {
		return 0
	}
	if iface != "" {
		for _, ifi := range ifaces {
			if ifi.Name == iface {
				return ifi.MTU
			}
		}
		return 0
	}
	// Try to find the interface that can reach dest by checking routes
	// Heuristic: match the interface whose subnet contains dest, or default
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
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
			if ipnet.Contains(dest) {
				return iface.MTU
			}
		}
	}
	// Fallback: return the MTU of the first non-loopback interface
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			return iface.MTU
		}
	}
	return 0
}

// ── DNS Checks ──

// DNSRecord holds a single DNS record result.
type DNSRecord struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
	TTL   uint32 `json:"ttl"`
}

// DNSServerResult holds the result of querying a single DNS server.
type DNSServerResult struct {
	Server    string      `json:"server"`
	Latency   float64     `json:"latency_ms"`
	Records   []DNSRecord `json:"records"`
	RCode     string      `json:"rcode"`
	Error     string      `json:"error,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	AD        bool        `json:"ad"`
}

// DNSCheckResult is the full DNS check result.
type DNSCheckResult struct {
	Domain       string            `json:"domain"`
	Type         string            `json:"type"`
	Servers      []DNSServerResult `json:"servers"`
	ResolverInfo *ResolverInfo     `json:"resolver_info,omitempty"`
	Timestamp    int64             `json:"timestamp"`
}

// Default DNS servers to test against.
var defaultDNSServers = []struct {
	Addr  string
	Label string
}{
	{"system", "System Resolver"},
	{"5.1.66.255:53", "FFMUC Anycast01 (5.1.66.255)"},
	{"[2001:678:e68:f000::]:53", "FFMUC Anycast01 (2001:678:e68:f000::)"},
	{"185.150.99.255:53", "FFMUC Anycast02 (185.150.99.255)"},
	{"[2001:678:ed0:f000::]:53", "FFMUC Anycast02 (2001:678:ed0:f000::)"},
	{"1.1.1.1:53", "Cloudflare (1.1.1.1)"},
	{"[2606:4700:4700::1111]:53", "Cloudflare (2606:4700:4700::1111)"},
	{"8.8.8.8:53", "Google (8.8.8.8)"},
	{"[2001:4860:4860::8888]:53", "Google (2001:4860:4860::8888)"},
	{"9.9.9.9:53", "Quad9 (9.9.9.9)"},
	{"[2620:fe::fe]:53", "Quad9 (2620:fe::fe)"},
	{"208.67.222.222:53", "OpenDNS (208.67.222.222)"},
	{"[2620:119:35::35]:53", "OpenDNS (2620:119:35::35)"},
}

// ResolverInfo holds information about the detected resolver.
type ResolverInfo struct {
	ConfiguredResolver string            `json:"configured_resolver"`     // what /etc/resolv.conf points to
	ResolverIPs        []string          `json:"resolver_ips"`            // public IPs seen by authoritative servers
	ECS                []string          `json:"ecs,omitempty"`           // EDNS Client Subnet info
	DNSCheckInfo       map[string]string `json:"dnscheck_info,omitempty"` // fields from dnscheck.tools
	Error              string            `json:"error,omitempty"`
}

// RunDNSCheck queries a domain against multiple DNS servers.
func RunDNSCheck(domain string, qtype string) *DNSCheckResult {
	if !strings.HasSuffix(domain, ".") {
		domain = domain + "."
	}

	var dnsType uint16
	switch strings.ToUpper(qtype) {
	case "AAAA":
		dnsType = mdns.TypeAAAA
	case "MX":
		dnsType = mdns.TypeMX
	case "TXT":
		dnsType = mdns.TypeTXT
	case "NS":
		dnsType = mdns.TypeNS
	case "CNAME":
		dnsType = mdns.TypeCNAME
	case "SOA":
		dnsType = mdns.TypeSOA
	case "PTR":
		dnsType = mdns.TypePTR
	default:
		dnsType = mdns.TypeA
		qtype = "A"
	}

	result := &DNSCheckResult{
		Domain:    strings.TrimSuffix(domain, "."),
		Type:      strings.ToUpper(qtype),
		Timestamp: time.Now().UnixMilli(),
	}

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, server := range defaultDNSServers {
		wg.Add(1)
		go func(addr, label string) {
			defer wg.Done()

			var sr DNSServerResult

			if addr == "system" {
				sr = querySystemDNS(domain, dnsType)
			} else {
				sr = queryDNSServer(domain, dnsType, addr)
				sr.Server = label
			}

			mu.Lock()
			result.Servers = append(result.Servers, sr)
			mu.Unlock()
		}(server.Addr, server.Label)
	}

	// Also run resolver leak check in parallel
	wg.Add(1)
	var resolverInfo *ResolverInfo
	go func() {
		defer wg.Done()
		ri := detectResolvers()
		mu.Lock()
		resolverInfo = ri
		mu.Unlock()
	}()

	wg.Wait()
	result.ResolverInfo = resolverInfo
	return result
}

// detectResolvers discovers which DNS resolver IPs the system is actually using
// by querying well-known "whoami" DNS services via both the system resolver
// and direct queries to force IPv4 and IPv6 paths.
func detectResolvers() *ResolverInfo {
	ri := &ResolverInfo{}
	var ips []string
	var ecs []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Read the configured resolver from /etc/resolv.conf
	config, cfgErr := mdns.ClientConfigFromFile("/etc/resolv.conf")
	if cfgErr == nil && len(config.Servers) > 0 {
		ri.ConfiguredResolver = config.Servers[0]
	} else {
		ri.ConfiguredResolver = "unknown"
	}

	// Method 1: Google o-o.myaddr.l.google.com TXT via system resolver
	// We use miekg/dns directly because Go's net.LookupTXT concatenates
	// multi-string TXT records (e.g. "185.150.99.3" "edns0-client-subnet ..."
	// becomes one garbled string). miekg/dns preserves individual strings.
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Query the system resolver (127.0.0.53 or /etc/resolv.conf)
		config, err := mdns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil {
			return
		}
		server := config.Servers[0] + ":" + config.Port

		msg := new(mdns.Msg)
		msg.SetQuestion("o-o.myaddr.l.google.com.", mdns.TypeTXT)
		msg.RecursionDesired = true
		client := &mdns.Client{Timeout: 5 * time.Second}
		resp, _, err := client.Exchange(msg, server)
		if err != nil {
			return
		}
		mu.Lock()
		for _, rr := range resp.Answer {
			if txt, ok := rr.(*mdns.TXT); ok {
				for _, s := range txt.Txt {
					s = strings.TrimSpace(s)
					if strings.HasPrefix(s, "edns0-client-subnet") {
						ecs = append(ecs, s)
					} else if ip := net.ParseIP(s); ip != nil {
						ips = append(ips, ip.String())
					}
				}
			}
		}
		mu.Unlock()
	}()

	// Method 2: dnscheck.tools TXT via system resolver (using miekg/dns)
	// Returns structured fields: "resolver: IP", "resolverGeo: ...", "resolverOrg: ...", "proto: ...", "edns0: ..."
	dnsCheckInfo := make(map[string]string)
	wg.Add(1)
	go func() {
		defer wg.Done()
		records := lookupTXTViaResolver("test.dnscheck.tools.")
		mu.Lock()
		for _, txt := range records {
			txt = strings.TrimSpace(txt)
			if idx := strings.Index(txt, ": "); idx > 0 {
				key := txt[:idx]
				val := txt[idx+2:]
				dnsCheckInfo[key] = val
				if key == "resolver" {
					if ip := net.ParseIP(val); ip != nil {
						ips = append(ips, ip.String())
					}
				}
			}
		}
		mu.Unlock()
	}()

	// Method 4: dnscheck.tools IPv4-only and IPv6-only variants
	for _, variant := range []string{"test-ipv4.dnscheck.tools.", "test-ipv6.dnscheck.tools."} {
		wg.Add(1)
		go func(domain string) {
			defer wg.Done()
			records := lookupTXTViaResolver(domain)
			mu.Lock()
			for _, txt := range records {
				txt = strings.TrimSpace(txt)
				if strings.HasPrefix(txt, "resolver: ") {
					val := strings.TrimPrefix(txt, "resolver: ")
					if ip := net.ParseIP(val); ip != nil {
						ips = append(ips, ip.String())
					}
				}
			}
			mu.Unlock()
		}(variant)
	}

	wg.Wait()

	// Deduplicate
	seen := make(map[string]bool)
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if ip != "" && !seen[ip] {
			seen[ip] = true
			ri.ResolverIPs = append(ri.ResolverIPs, ip)
		}
	}

	if len(ri.ResolverIPs) == 0 {
		ri.Error = "could not detect resolver IPs"
	}

	// Deduplicate ECS
	esSeen := make(map[string]bool)
	for _, e := range ecs {
		if !esSeen[e] {
			esSeen[e] = true
			ri.ECS = append(ri.ECS, e)
		}
	}

	// Attach dnscheck.tools structured info
	if len(dnsCheckInfo) > 0 {
		ri.DNSCheckInfo = dnsCheckInfo
	}

	return ri
}

// lookupTXTViaResolver queries TXT records using miekg/dns via the system resolver
// (from /etc/resolv.conf). This avoids Go's net.LookupTXT which concatenates
// multi-string TXT records into a single string.
func lookupTXTViaResolver(fqdn string) []string {
	config, err := mdns.ClientConfigFromFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	server := config.Servers[0] + ":" + config.Port

	msg := new(mdns.Msg)
	msg.SetQuestion(fqdn, mdns.TypeTXT)
	msg.RecursionDesired = true
	client := &mdns.Client{Timeout: 5 * time.Second}
	resp, _, err := client.Exchange(msg, server)
	if err != nil {
		return nil
	}

	var results []string
	for _, rr := range resp.Answer {
		if txt, ok := rr.(*mdns.TXT); ok {
			results = append(results, txt.Txt...)
		}
	}
	return results
}

func querySystemDNS(domain string, qtype uint16) DNSServerResult {
	sr := DNSServerResult{Server: "System Resolver"}
	start := time.Now()

	cleanDomain := strings.TrimSuffix(domain, ".")

	switch qtype {
	case mdns.TypeA:
		ips, err := net.LookupHost(cleanDomain)
		sr.Latency = float64(time.Since(start).Microseconds()) / 1000.0
		if err != nil {
			sr.Error = sanitizeError(err)
			sr.RCode = "ERROR"
			return sr
		}
		sr.RCode = "NOERROR"
		for _, ip := range ips {
			parsed := net.ParseIP(ip)
			if parsed != nil && parsed.To4() != nil {
				sr.Records = append(sr.Records, DNSRecord{Type: "A", Name: cleanDomain, Value: ip, TTL: 0})
			}
		}
	case mdns.TypeAAAA:
		ips, err := net.LookupHost(cleanDomain)
		sr.Latency = float64(time.Since(start).Microseconds()) / 1000.0
		if err != nil {
			sr.Error = sanitizeError(err)
			sr.RCode = "ERROR"
			return sr
		}
		sr.RCode = "NOERROR"
		for _, ip := range ips {
			parsed := net.ParseIP(ip)
			if parsed != nil && parsed.To4() == nil {
				sr.Records = append(sr.Records, DNSRecord{Type: "AAAA", Name: cleanDomain, Value: ip, TTL: 0})
			}
		}
	case mdns.TypeMX:
		mxs, err := net.LookupMX(cleanDomain)
		sr.Latency = float64(time.Since(start).Microseconds()) / 1000.0
		if err != nil {
			sr.Error = sanitizeError(err)
			sr.RCode = "ERROR"
			return sr
		}
		sr.RCode = "NOERROR"
		for _, mx := range mxs {
			sr.Records = append(sr.Records, DNSRecord{Type: "MX", Name: cleanDomain, Value: fmt.Sprintf("%d %s", mx.Pref, mx.Host), TTL: 0})
		}
	case mdns.TypeTXT:
		txts, err := net.LookupTXT(cleanDomain)
		sr.Latency = float64(time.Since(start).Microseconds()) / 1000.0
		if err != nil {
			sr.Error = sanitizeError(err)
			sr.RCode = "ERROR"
			return sr
		}
		sr.RCode = "NOERROR"
		for _, txt := range txts {
			sr.Records = append(sr.Records, DNSRecord{Type: "TXT", Name: cleanDomain, Value: txt, TTL: 0})
		}
	case mdns.TypeNS:
		nss, err := net.LookupNS(cleanDomain)
		sr.Latency = float64(time.Since(start).Microseconds()) / 1000.0
		if err != nil {
			sr.Error = sanitizeError(err)
			sr.RCode = "ERROR"
			return sr
		}
		sr.RCode = "NOERROR"
		for _, ns := range nss {
			sr.Records = append(sr.Records, DNSRecord{Type: "NS", Name: cleanDomain, Value: ns.Host, TTL: 0})
		}
	default:
		// Fall back to A lookup for system resolver
		ips, err := net.LookupHost(cleanDomain)
		sr.Latency = float64(time.Since(start).Microseconds()) / 1000.0
		if err != nil {
			sr.Error = sanitizeError(err)
			sr.RCode = "ERROR"
			return sr
		}
		sr.RCode = "NOERROR"
		for _, ip := range ips {
			sr.Records = append(sr.Records, DNSRecord{Type: "A", Name: cleanDomain, Value: ip, TTL: 0})
		}
	}

	return sr
}

func queryDNSServer(domain string, qtype uint16, server string) DNSServerResult {
	sr := DNSServerResult{Server: server}

	msg := new(mdns.Msg)
	msg.SetQuestion(domain, qtype)
	msg.RecursionDesired = true

	client := &mdns.Client{Timeout: 5 * time.Second}

	start := time.Now()
	resp, _, err := client.Exchange(msg, server)
	sr.Latency = float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		sr.Error = sanitizeError(err)
		sr.RCode = "ERROR"
		return sr
	}

	sr.RCode = mdns.RcodeToString[resp.Rcode]
	sr.Truncated = resp.Truncated
	sr.AD = resp.AuthenticatedData

	for _, rr := range resp.Answer {
		rec := DNSRecord{
			Name: rr.Header().Name,
			TTL:  rr.Header().Ttl,
		}
		switch v := rr.(type) {
		case *mdns.A:
			rec.Type = "A"
			rec.Value = v.A.String()
		case *mdns.AAAA:
			rec.Type = "AAAA"
			rec.Value = v.AAAA.String()
		case *mdns.CNAME:
			rec.Type = "CNAME"
			rec.Value = v.Target
		case *mdns.MX:
			rec.Type = "MX"
			rec.Value = fmt.Sprintf("%d %s", v.Preference, v.Mx)
		case *mdns.TXT:
			rec.Type = "TXT"
			rec.Value = strings.Join(v.Txt, " ")
		case *mdns.NS:
			rec.Type = "NS"
			rec.Value = v.Ns
		case *mdns.SOA:
			rec.Type = "SOA"
			rec.Value = fmt.Sprintf("%s %s %d %d %d %d %d", v.Ns, v.Mbox, v.Serial, v.Refresh, v.Retry, v.Expire, v.Minttl)
		case *mdns.PTR:
			rec.Type = "PTR"
			rec.Value = v.Ptr
		default:
			rec.Type = mdns.TypeToString[rr.Header().Rrtype]
			rec.Value = rr.String()
		}
		sr.Records = append(sr.Records, rec)
	}

	return sr
}
