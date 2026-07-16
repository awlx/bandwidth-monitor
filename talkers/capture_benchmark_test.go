package talkers

import (
	"net"
	"testing"
)

func BenchmarkPacketEndpointClassification(b *testing.B) {
	localNets := make([]*net.IPNet, 0, 21)
	for i := 0; i < 21; i++ {
		localNets = append(localNets, &net.IPNet{
			IP:   net.IPv4(10, byte(i), 0, 0),
			Mask: net.CIDRMask(16, 32),
		})
	}
	tracker := &Tracker{
		localNets: localNets,
		selfIPs:   map[string]struct{}{"10.5.0.1": {}},
	}
	src := net.ParseIP("10.5.0.42")
	dst := net.ParseIP("203.0.113.10")
	cache := make(map[[16]byte]*packetEndpoint)

	b.ReportAllocs()
	for b.Loop() {
		_ = tracker.classifyEndpoint(src, cache)
		_ = tracker.classifyEndpoint(dst, cache)
	}
}

func BenchmarkAccountPacketCachedEndpoints(b *testing.B) {
	tracker := &Tracker{}
	current := &bucket{hosts: make(map[string]*hostAccum)}
	rates := &rateSlot{hosts: make(map[string]*hostAccum)}
	packet := &parsedPkt{
		srcStr:      "192.168.0.10",
		dstStr:      "203.0.113.10",
		srcEndpoint: &packetEndpoint{},
		dstEndpoint: &packetEndpoint{},
		wireLen:     1500,
	}

	tracker.accountPacket(packet, current, rates)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tracker.accountPacket(packet, current, rates)
	}
}

func BenchmarkAccountPacketMapLookups(b *testing.B) {
	tracker := &Tracker{}
	current := &bucket{hosts: make(map[string]*hostAccum)}
	rates := &rateSlot{hosts: make(map[string]*hostAccum)}
	packet := &parsedPkt{
		srcStr:  "192.168.0.10",
		dstStr:  "203.0.113.10",
		wireLen: 1500,
	}

	tracker.accountPacket(packet, current, rates)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		tracker.accountPacket(packet, current, rates)
	}
}

func TestAccountEndpointRefreshesAfterRotation(t *testing.T) {
	endpoint := &packetEndpoint{}
	firstBucket := &bucket{hosts: make(map[string]*hostAccum)}
	firstRate := &rateSlot{hosts: make(map[string]*hostAccum)}
	secondBucket := &bucket{hosts: make(map[string]*hostAccum)}
	secondRate := &rateSlot{hosts: make(map[string]*hostAccum)}

	firstCurrent, firstRateAccum := accountEndpoint(endpoint, "192.0.2.1", false, firstBucket, firstRate)
	secondCurrent, secondRateAccum := accountEndpoint(endpoint, "192.0.2.1", false, secondBucket, secondRate)

	if firstCurrent == secondCurrent || firstRateAccum == secondRateAccum {
		t.Fatal("rotated bucket or rate slot reused a stale accumulator")
	}
	if secondBucket.hosts["192.0.2.1"] != secondCurrent || secondRate.hosts["192.0.2.1"] != secondRateAccum {
		t.Fatal("new accumulators were not registered in the rotated maps")
	}
}

func TestPacketIPKeyNormalizesIPv4(t *testing.T) {
	short := net.IP{192, 0, 2, 1}
	parsed := net.ParseIP("192.0.2.1")
	if packetIPKey(short) != packetIPKey(parsed) {
		t.Fatal("4-byte and 16-byte representations produced different cache keys")
	}
}

func TestClassifyEndpointCachesMetadata(t *testing.T) {
	tracker := &Tracker{
		localNets: []*net.IPNet{mustCIDR(t, "192.168.0.0/24")},
		selfIPs:   map[string]struct{}{"192.168.0.1": {}},
	}
	cache := make(map[[16]byte]*packetEndpoint)

	got := tracker.classifyEndpoint(net.ParseIP("192.168.0.1"), cache)
	if got.str != "192.168.0.1" || !got.local || !got.self || got.loopLL {
		t.Fatalf("unexpected endpoint metadata: %+v", got)
	}

	tracker.localNets = nil
	tracker.selfIPs = nil
	cached := tracker.classifyEndpoint(net.ParseIP("192.168.0.1"), cache)
	if cached != got {
		t.Fatal("classifyEndpoint did not return the cached endpoint")
	}
}
