package talkers

import (
	"fmt"
	"math/rand"
	"net"
	"testing"
	"time"
)

func TestClassifyService(t *testing.T) {
	cases := []struct {
		name     string
		proto    uint8
		src, dst uint16
		hasPorts bool
		want     string
	}{
		{"https client to server", protocolTCP, 51000, 443, true, "HTTPS"},
		{"https server to client", protocolTCP, 443, 51000, true, "HTTPS"},
		{"quic", protocolUDP, 443, 62000, true, "QUIC"},
		{"http", protocolTCP, 40000, 80, true, "HTTP"},
		{"http alt", protocolTCP, 40000, 8080, true, "HTTP"},
		{"dns udp", protocolUDP, 53, 33333, true, "DNS"},
		{"dns tcp", protocolTCP, 33333, 53, true, "DNS"},
		{"dot", protocolTCP, 40000, 853, true, "DoT"},
		{"doq", protocolUDP, 40000, 853, true, "DoQ"},
		{"ssh", protocolTCP, 22, 50000, true, "SSH"},
		{"ntp", protocolUDP, 123, 123, true, "NTP"},
		{"wireguard high client port", protocolUDP, 40000, 51820, true, "WireGuard"},
		{"openvpn", protocolUDP, 1194, 50000, true, "OpenVPN"},
		{"ike", protocolUDP, 500, 500, true, "IPsec"},
		{"ipsec nat-t", protocolUDP, 4500, 60000, true, "IPsec"},
		{"esp", ipProtoESP, 0, 0, false, "IPsec"},
		{"smtp submission", protocolTCP, 50000, 587, true, "Mail"},
		{"imaps", protocolTCP, 993, 50000, true, "Mail"},
		{"lower known port wins", protocolTCP, 443, 8080, true, "HTTPS"},
		{"unknown tcp", protocolTCP, 40000, 40001, true, "Other TCP"},
		{"unknown udp", protocolUDP, 40000, 40001, true, "Other UDP"},
		{"udp 22 is not ssh", protocolUDP, 22, 40000, true, "Other UDP"},
		{"tcp without ports", protocolTCP, 0, 0, false, "Other TCP"},
		{"icmp", ipProtoICMP, 0, 0, false, "ICMP"},
		{"icmpv6", ipProtoICMPv6, 0, 0, false, "ICMP"},
		{"gre", 47, 0, 0, false, "Other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := serviceNames[classifyService(tc.proto, tc.src, tc.dst, tc.hasPorts)]
			if got != tc.want {
				t.Fatalf("classifyService = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestServiceNamesComplete(t *testing.T) {
	seen := map[string]bool{}
	for i, n := range serviceNames {
		if n == "" {
			t.Fatalf("service %d has no name", i)
		}
		if seen[n] {
			t.Fatalf("duplicate service name %q", n)
		}
		seen[n] = true
	}
}

func rawL4(version int, proto uint8, srcPort, dstPort uint16) []byte {
	var pkt []byte
	off := 20
	if version == 4 {
		pkt = make([]byte, 40)
		pkt[0] = 0x45
		pkt[2], pkt[3] = 0, 40
		pkt[9] = proto
		copy(pkt[12:16], net.IPv4(10, 0, 0, 2).To4())
		copy(pkt[16:20], net.IPv4(198, 51, 100, 1).To4())
	} else {
		pkt = make([]byte, 60)
		pkt[0] = 0x60
		pkt[5] = 20
		pkt[6] = proto
		copy(pkt[8:24], net.ParseIP("fd00::2"))
		copy(pkt[24:40], net.ParseIP("2001:db8::1"))
		off = 40
	}
	pkt[off], pkt[off+1] = byte(srcPort>>8), byte(srcPort)
	pkt[off+2], pkt[off+3] = byte(dstPort>>8), byte(dstPort)
	if proto == protocolTCP {
		pkt[off+12] = 0x50
	} else if proto == protocolUDP {
		pkt[off+5] = 20
	}
	return pkt
}

func TestQuickL4Ports(t *testing.T) {
	proto, sp, dp, ok := quickL4Ports(rawL4(4, protocolUDP, 50000, 443))
	if !ok || proto != protocolUDP || sp != 50000 || dp != 443 {
		t.Fatalf("ipv4 udp = %d %d %d %v", proto, sp, dp, ok)
	}
	proto, sp, dp, ok = quickL4Ports(rawL4(6, protocolTCP, 443, 50000))
	if !ok || proto != protocolTCP || sp != 443 || dp != 50000 {
		t.Fatalf("ipv6 tcp = %d %d %d %v", proto, sp, dp, ok)
	}
	frag := rawL4(4, protocolUDP, 50000, 443)
	frag[7] = 1 // fragment offset != 0: no L4 header in this packet
	if _, _, _, ok := quickL4Ports(frag); ok {
		t.Fatal("non-initial fragment returned ports")
	}
	if _, _, _, ok := quickL4Ports(rawL4(4, protocolTCP, 1, 2)[:22]); ok {
		t.Fatal("truncated packet returned ports")
	}
	if p, _, _, ok := quickL4Ports(rawL4(4, ipProtoICMP, 0, 0)); ok || p != ipProtoICMP {
		t.Fatalf("icmp = %d %v", p, ok)
	}
}

// TestParsePacketService checks the service classification in both the
// standard and the direct capture path.
func TestParsePacketService(t *testing.T) {
	_, lan, _ := net.ParseCIDR("10.0.0.0/8")
	for _, direct := range []bool{false, true} {
		tr := &Tracker{localNets: []*net.IPNet{lan}, direct: direct}
		cache := make(map[[16]byte]*packetEndpoint)
		for _, tc := range []struct {
			pkt  []byte
			want string
		}{
			{rawL4(4, protocolUDP, 50000, 443), "QUIC"},
			{rawL4(4, protocolTCP, 50000, 443), "HTTPS"},
			{rawL4(6, protocolUDP, 53, 40000), "DNS"},
			{rawL4(6, protocolTCP, 40000, 40001), "Other TCP"},
		} {
			p, ok := tr.parsePacket(tc.pkt, uint32(len(tc.pkt)), cache)
			if !ok {
				t.Fatalf("direct=%v: packet dropped", direct)
			}
			if got := serviceNames[p.service]; got != tc.want {
				t.Fatalf("direct=%v: service = %q, want %q", direct, got, tc.want)
			}
		}
	}
}

func TestParsePacketServiceNoAlloc(t *testing.T) {
	_, lan, _ := net.ParseCIDR("10.0.0.0/8")
	tr := &Tracker{localNets: []*net.IPNet{lan}}
	cache := make(map[[16]byte]*packetEndpoint)
	pkt := rawL4(4, protocolUDP, 50000, 443)
	tr.parsePacket(pkt, 40, cache)
	if n := testing.AllocsPerRun(1000, func() { tr.parsePacket(pkt, 40, cache) }); n != 0 {
		t.Fatalf("parsePacket allocates %.1f times per packet", n)
	}
}

// TestServiceTotalsMatchBuckets checks that the 24h service totals equal a
// full recomputation over the retained buckets across rotation and expiry.
func TestServiceTotalsMatchBuckets(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	tr := scopedRankingTracker()
	now := time.Now().Truncate(bucketSize)
	tr.current = rankingBucket(nil)
	tr.current.timestamp = now

	for step := 0; step < 3000; step++ {
		for i := 0; i < 10; i++ {
			tr.current.serviceBytes[rng.Intn(int(numServices))] += uint64(rng.Intn(1500) + 1)
		}
		now = now.Add(bucketSize)
		next := rankingBucket(nil)
		next.timestamp = now
		tr.rotateBucketLocked(now, next)
		if step%250 != 0 && step != 2999 {
			continue
		}
		want := map[string]uint64{}
		for _, b := range tr.buckets {
			for i, v := range b.serviceBytes {
				if v > 0 {
					want[serviceNames[i]] += v
				}
			}
		}
		if got := tr.GetServiceBreakdown(); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("step %d: services = %v, want %v", step, got, want)
		}
	}
}

func TestApplyBatchServiceBytes(t *testing.T) {
	_, lan, _ := net.ParseCIDR("10.0.0.0/8")
	tr := &Tracker{localNets: []*net.IPNet{lan}}
	tr.current = rankingBucket(nil)
	tr.rateRing[0] = &rateSlot{timestamp: time.Now(), hosts: make(map[string]*hostAccum)}
	cache := make(map[[16]byte]*packetEndpoint)
	var batch []parsedPkt
	for _, pkt := range [][]byte{rawL4(4, protocolUDP, 50000, 443), rawL4(4, protocolUDP, 50001, 443), rawL4(4, protocolTCP, 50000, 22)} {
		p, _ := tr.parsePacket(pkt, 1000, cache)
		batch = append(batch, p)
	}
	tr.applyBatch(batch)
	got := tr.GetServiceBreakdown()
	if got["QUIC"] != 2000 || got["SSH"] != 1000 || len(got) != 2 {
		t.Fatalf("services = %v", got)
	}
}
