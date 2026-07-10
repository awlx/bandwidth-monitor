package talkers

import (
	"net"
	"testing"
	"time"
)

func TestDirectPeerAggregationUsesActualLocalEndpointAndDirection(t *testing.T) {
	now := time.Unix(100, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]

	// LAN forwarding: the client, not the router interface, is the local endpoint.
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.0.2.20", true, false, 100), slot)
	tracker.accountDirectPeer(directPacket("192.0.2.20", "192.168.1.20", false, true, 200), slot)
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.0.2.20", true, false, 50), slot)
	// Router-originated traffic keeps the router's actual source address.
	tracker.accountDirectPeer(directPacket("192.168.1.1", "198.51.100.30", true, false, 75), slot)

	got, totals := tracker.directBandwidthSnapshot(10, now)
	if len(got) != 2 {
		t.Fatalf("got %d peer rows: %+v", len(got), got)
	}
	assertDirectStat(t, got[0], "192.168.1.20", "192.0.2.20", 200, 150, 3)
	assertDirectStat(t, got[1], "192.168.1.1", "198.51.100.30", 0, 75, 1)
	if totals.RxRate != 100 || totals.TxRate != 112.5 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}

func TestDirectRollingWindowsAndAllPeerTotals(t *testing.T) {
	now := time.Unix(200, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	pair := directPairKey{local: "192.168.1.20", remote: "192.0.2.20"}
	tracker.directRateRing[0].peers[pair] = &hostAccum{bytes: 300, rxBytes: 100, txBytes: 200, packets: 3}
	tracker.directRateRing[1] = &directRateSlot{
		timestamp: now.Add(-10 * time.Second),
		peers: map[directPairKey]*hostAccum{
			pair: {bytes: 700, rxBytes: 300, txBytes: 400, packets: 7},
		},
	}
	tracker.directRateRing[2] = &directRateSlot{
		timestamp: now.Add(-40 * time.Second),
		peers: map[directPairKey]*hostAccum{
			pair: {bytes: 3000, rxBytes: 1000, txBytes: 2000, packets: 30},
		},
	}

	got, totals := tracker.directBandwidthSnapshot(10, now)
	if len(got) != 1 {
		t.Fatalf("got %+v", got)
	}
	stat := got[0]
	if stat.RxRate != 50 || stat.TxRate != 100 ||
		stat.RxRate10 != 40 || stat.TxRate10 != 60 ||
		stat.RxRate40 != 35 || stat.TxRate40 != 65 {
		t.Fatalf("unexpected rolling rates: %+v", stat)
	}
	if totals.RxRate != stat.RxRate || totals.TxRate40 != stat.TxRate40 {
		t.Fatalf("unexpected totals: %+v", totals)
	}
}

func TestDirectPeerAggregationExcludesAmbiguousEndpointClasses(t *testing.T) {
	now := time.Unix(300, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.168.1.30", true, true, 100), slot)
	tracker.accountDirectPeer(directPacket("192.0.2.20", "198.51.100.30", false, false, 100), slot)
	if got, _ := tracker.directBandwidthSnapshot(10, now); len(got) != 0 {
		t.Fatalf("ambiguous local-local or remote-remote packet produced rows: %+v", got)
	}
}

func TestDirectLocalClassificationUsesOnlyCapturePrefixes(t *testing.T) {
	tracker := directTrackerWithNetworks(
		"192.168.1.0/24",
		"203.0.113.16/28",
		"2001:db8:1::/64",
		"198.51.100.9/32",
		"2001:db8:2::9/128",
	)
	cases := []struct {
		ip    string
		local bool
	}{
		{"192.168.1.20", true},
		{"10.0.0.1", false},
		{"192.168.2.20", false},
		{"203.0.113.20", true},
		{"203.0.113.40", false},
		{"2001:db8:1::20", true},
		{"fd00::20", false},
		{"fe80::20", false},
		{"198.51.100.9", true},
		{"198.51.100.10", false},
		{"2001:db8:2::9", true},
		{"2001:db8:2::10", false},
	}
	for _, test := range cases {
		if got := tracker.packetIPIsLocal(net.ParseIP(test.ip)); got != test.local {
			t.Fatalf("%s local=%v, want %v", test.ip, got, test.local)
		}
	}
	empty := &Tracker{direct: true}
	if empty.packetIPIsLocal(net.ParseIP("192.168.1.20")) {
		t.Fatal("direct capture fell back to RFC1918 locality with no prefixes")
	}
}

func TestDirectAggregationUsesTopologyClassification(t *testing.T) {
	now := time.Unix(350, 0)
	tracker := directTrackerWithNetworks("192.168.1.0/24")
	tracker.current = &bucket{
		hosts: make(map[string]*hostAccum), protoBytes: make(map[string]uint64),
		ipVerBytes: make(map[string]uint64), pairs: make(map[string]map[string]*hostAccum),
	}
	tracker.directRateRing[0] = &directRateSlot{
		timestamp: now.Add(-2 * time.Second),
		peers:     make(map[directPairKey]*hostAccum),
	}
	slot := tracker.directRateRing[0]
	tracker.accountDirectPeer(classifiedDirectPacket(tracker, "192.168.1.20", "10.0.0.5", 100), slot)
	tracker.accountDirectPeer(classifiedDirectPacket(tracker, "10.0.0.5", "192.168.1.20", 200), slot)
	tracker.accountDirectPeer(classifiedDirectPacket(tracker, "10.0.0.5", "192.168.2.20", 300), slot)
	tracker.accountDirectPeer(classifiedDirectPacket(tracker, "192.168.1.20", "192.168.1.30", 400), slot)
	got, _ := tracker.directBandwidthSnapshot(10, now)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want one topology-defined pair: %+v", len(got), got)
	}
	assertDirectStat(t, got[0], "192.168.1.20", "10.0.0.5", 200, 100, 2)
}

func TestDirectPeerTopNSortsByCombinedTwoSecondRate(t *testing.T) {
	now := time.Unix(400, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.0.2.20", true, false, 100), slot)
	tracker.accountDirectPeer(directPacket("192.0.2.20", "192.168.1.20", false, true, 100), slot)
	tracker.accountDirectPeer(directPacket("192.168.1.30", "198.51.100.30", true, false, 50), slot)

	got, totals := tracker.directBandwidthSnapshot(1, now)
	if len(got) != 1 || got[0].LocalIP != "192.168.1.20" || got[0].IP != "192.0.2.20" {
		t.Fatalf("unexpected top peer: %+v", got)
	}
	if totals.TxRate != 75 || totals.RxRate != 50 {
		t.Fatalf("totals excluded rows below top-N: %+v", totals)
	}
}

func TestDirectPeerSortTieIsDeterministic(t *testing.T) {
	now := time.Unix(500, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	tracker.accountDirectPeer(directPacket("192.168.1.30", "198.51.100.30", true, false, 100), slot)
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.0.2.20", true, false, 100), slot)
	got, _ := tracker.directBandwidthSnapshot(10, now)
	if len(got) != 2 || got[0].IP != "192.0.2.20" {
		t.Fatalf("nondeterministic tie order: %+v", got)
	}
}

func TestDirectSnapshotWithoutResolverDoesNotResolveHostname(t *testing.T) {
	now := time.Unix(600, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	tracker.accountDirectPeer(
		directPacket("192.168.1.20", "192.0.2.20", true, false, 100),
		tracker.directRateRing[0],
	)
	got, _ := tracker.directBandwidthSnapshot(1, now)
	if len(got) != 1 || got[0].Hostname != "" {
		t.Fatalf("snapshot unexpectedly resolved hostname without resolver: %+v", got)
	}
}

func directAggregationTracker(start time.Time) *Tracker {
	_, localNet, _ := net.ParseCIDR("192.168.1.0/24")
	tracker := &Tracker{
		direct:    true,
		localNets: []*net.IPNet{localNet},
		current: &bucket{
			hosts:      make(map[string]*hostAccum),
			protoBytes: make(map[string]uint64),
			ipVerBytes: make(map[string]uint64),
			pairs:      make(map[string]map[string]*hostAccum),
		},
	}

	tracker.directRateRing[0] = &directRateSlot{
		timestamp: start,
		peers:     make(map[directPairKey]*hostAccum),
	}
	return tracker
}

func directTrackerWithNetworks(cidrs ...string) *Tracker {
	networks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		networks = append(networks, network)
	}
	return &Tracker{direct: true, localNets: networks}
}

func classifiedDirectPacket(tracker *Tracker, src, dst string, wireLen uint64) *parsedPkt {
	srcIP := net.ParseIP(src)
	dstIP := net.ParseIP(dst)
	return directPacket(src, dst, tracker.packetIPIsLocal(srcIP), tracker.packetIPIsLocal(dstIP), wireLen)
}

func directPacket(src, dst string, srcLocal, dstLocal bool, wireLen uint64) *parsedPkt {
	return &parsedPkt{
		srcStr: src, dstStr: dst, srcLocal: srcLocal, dstLocal: dstLocal,
		wireLen: wireLen,
	}
}

func assertDirectStat(t *testing.T, got DirectTalkerStat, local, remote string, rx, tx, packets uint64) {
	t.Helper()
	if got.LocalIP != local || got.IP != remote || got.RxBytes != rx ||
		got.TxBytes != tx || got.TotalBytes != rx+tx || got.Packets != packets {
		t.Fatalf("got %+v, want local=%s remote=%s rx=%d tx=%d packets=%d",
			got, local, remote, rx, tx, packets)
	}
}
