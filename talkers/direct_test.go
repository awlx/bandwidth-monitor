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
