package talkers

import (
	"net"
	"testing"
	"time"
)

func TestDirectPeerAggregationUsesActualLocalEndpointAndDirection(t *testing.T) {
	tracker := directAggregationTracker()
	slot := tracker.rateRing[0]

	// LAN forwarding: the client, not the router interface, is the local endpoint.
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.0.2.20", true, false, 100), slot)
	tracker.accountDirectPeer(directPacket("192.0.2.20", "192.168.1.20", false, true, 200), slot)
	// Repeated traffic aggregates into the same peer row.
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.0.2.20", true, false, 50), slot)
	// Router-originated traffic keeps the router's actual source address.
	tracker.accountDirectPeer(directPacket("192.168.1.1", "198.51.100.30", true, false, 75), slot)

	got := tracker.DirectTopByBandwidth(10)
	if len(got) != 2 {
		t.Fatalf("got %d peer rows: %+v", len(got), got)
	}
	assertDirectStat(t, got[0], "192.168.1.20", "192.0.2.20", 200, 150, 3)
	assertDirectStat(t, got[1], "192.168.1.1", "198.51.100.30", 0, 75, 1)
}

func TestDirectPeerAggregationExcludesAmbiguousEndpointClasses(t *testing.T) {
	tracker := directAggregationTracker()
	slot := tracker.rateRing[0]
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.168.1.30", true, true, 100), slot)
	tracker.accountDirectPeer(directPacket("192.0.2.20", "198.51.100.30", false, false, 100), slot)
	if got := tracker.DirectTopByBandwidth(10); len(got) != 0 {
		t.Fatalf("ambiguous local-local or remote-remote packet produced rows: %+v", got)
	}
}

func TestDirectPeerTopNCountsPairsNotDuplicatedEndpoints(t *testing.T) {
	tracker := directAggregationTracker()
	slot := tracker.rateRing[0]
	tracker.accountDirectPeer(directPacket("192.168.1.20", "192.0.2.20", true, false, 100), slot)
	tracker.accountDirectPeer(directPacket("192.0.2.20", "192.168.1.20", false, true, 100), slot)
	tracker.accountDirectPeer(directPacket("192.168.1.30", "198.51.100.30", true, false, 50), slot)

	got := tracker.DirectTopByBandwidth(1)
	if len(got) != 1 || got[0].LocalIP != "192.168.1.20" || got[0].IP != "192.0.2.20" {
		t.Fatalf("unexpected top peer: %+v", got)
	}
}

func directAggregationTracker() *Tracker {
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
	tracker.rateRing[0] = &rateSlot{
		timestamp:   time.Now().Add(-time.Second),
		hosts:       make(map[string]*hostAccum),
		directPeers: make(map[directPairKey]*hostAccum),
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
