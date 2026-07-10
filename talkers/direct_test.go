package talkers

import (
	"fmt"
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

func TestDirectPortAggregationUsesRemoteServiceAndProtocol(t *testing.T) {
	now := time.Unix(700, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	accountDirectTestFlow(tracker, flowPacket("192.168.1.20", "192.0.2.20", true, false, 100, "TCP", 50000, 443, true), slot)
	accountDirectTestFlow(tracker, flowPacket("192.168.1.20", "192.0.2.20", true, false, 150, "TCP", 50001, 443, true), slot)
	accountDirectTestFlow(tracker, flowPacket("192.0.2.20", "192.168.1.20", false, true, 200, "TCP", 443, 50000, true), slot)
	accountDirectTestFlow(tracker, flowPacket("192.168.1.20", "192.0.2.20", true, false, 50, "TCP", 50002, 80, true), slot)
	accountDirectTestFlow(tracker, flowPacket("192.168.1.20", "192.0.2.20", true, false, 75, "UDP", 50003, 443, true), slot)

	got, totals := tracker.directPortBandwidthSnapshot(10, now)
	if len(got) != 3 {
		t.Fatalf("got %d port rows: %+v", len(got), got)
	}
	assertDirectFlowStat(t, got[0], "TCP", 443, true, 200, 250, 3)
	assertDirectFlowStat(t, got[1], "UDP", 443, true, 0, 75, 1)
	assertDirectFlowStat(t, got[2], "TCP", 80, true, 0, 50, 1)
	_, hostTotals := tracker.directBandwidthSnapshot(10, now)
	if totals != hostTotals {
		t.Fatalf("port totals %+v differ from host totals %+v", totals, hostTotals)
	}
}

func TestDirectPortInboundDirectionAndUnknownPortBucket(t *testing.T) {
	now := time.Unix(800, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	accountDirectTestFlow(tracker, flowPacket("198.51.100.53", "192.168.1.20", false, true, 100, "UDP", 53, 60000, true), slot)
	accountDirectTestFlow(tracker, flowPacket("198.51.100.53", "192.168.1.20", false, true, 60, "TCP", 0, 0, false), slot)
	got, _ := tracker.directPortBandwidthSnapshot(10, now)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	assertDirectFlowStat(t, got[0], "UDP", 53, true, 100, 0, 1)
	assertDirectFlowStat(t, got[1], "TCP", 0, false, 60, 0, 1)
}

func TestDirectModeSwitchUsesContinuousRollingData(t *testing.T) {
	now := time.Unix(900, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	packet := flowPacket("192.168.1.20", "192.0.2.20", true, false, 200, "TCP", 50000, 443, true)
	accountDirectTestFlow(tracker, packet, tracker.directRateRing[0])

	hostsBefore, hostTotalsBefore := tracker.DirectBandwidthSnapshotForModeAt(DirectViewHosts, 10, now)
	ports, portTotals := tracker.DirectBandwidthSnapshotForModeAt(DirectViewPorts, 10, now)
	hostsAfter, hostTotalsAfter := tracker.DirectBandwidthSnapshotForModeAt(DirectViewHosts, 10, now)
	if len(hostsBefore) != 1 || len(ports) != 1 || len(hostsAfter) != 1 ||
		hostsBefore[0].TxRate != ports[0].TxRate || hostsAfter[0].TxRate != hostsBefore[0].TxRate ||
		hostTotalsBefore != portTotals || hostTotalsAfter != hostTotalsBefore {
		t.Fatalf("mode switch reset or changed data: hosts=%+v ports=%+v after=%+v totals=%+v/%+v/%+v",
			hostsBefore, ports, hostsAfter, hostTotalsBefore, portTotals, hostTotalsAfter)
	}
}

func TestDirectCumulativeTotalsAreSharedAndCountAcceptedPacketsOnce(t *testing.T) {
	now := time.Unix(950, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	for _, packet := range []*parsedPkt{
		flowPacket("192.168.1.20", "192.0.2.20", true, false, 100, "TCP", 50000, 443, true),
		flowPacket("192.0.2.20", "192.168.1.20", false, true, 200, "TCP", 443, 50000, true),
		flowPacket("192.168.1.20", "192.0.2.20", true, false, 50, "UDP", 50001, 53, true),
	} {
		accountDirectTestFlow(tracker, packet, slot)
	}
	for _, ambiguous := range []*parsedPkt{
		directPacket("192.168.1.20", "192.168.1.30", true, true, 999),
		directPacket("192.0.2.20", "198.51.100.30", false, false, 999),
	} {
		if tracker.accountDirectPeer(ambiguous, slot) {
			t.Fatalf("ambiguous packet was accepted: %+v", ambiguous)
		}
	}

	_, hostTotals := tracker.DirectBandwidthSnapshotForModeAt(DirectViewHosts, 10, now)
	_, portTotals := tracker.DirectBandwidthSnapshotForModeAt(DirectViewPorts, 10, now)
	if hostTotals.TxBytes != 150 || hostTotals.RxBytes != 200 {
		t.Fatalf("unexpected cumulative totals: %+v", hostTotals)
	}
	if hostTotals != portTotals {
		t.Fatalf("host and port totals differ: hosts=%+v ports=%+v", hostTotals, portTotals)
	}
	if hostTotals.TxBytes+hostTotals.RxBytes != 350 {
		t.Fatalf("combined bytes=%d", hostTotals.TxBytes+hostTotals.RxBytes)
	}
}

func TestDirectCumulativeTotalsStartAtZeroAndSaturate(t *testing.T) {
	now := time.Unix(975, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	_, totals := tracker.DirectBandwidthSnapshotForModeAt(DirectViewHosts, 10, now)
	if totals.RxBytes != 0 || totals.TxBytes != 0 {
		t.Fatalf("new tracker totals=%+v", totals)
	}

	const maxUint64 = ^uint64(0)
	tracker.directTxBytes = maxUint64 - 5
	tracker.directRxBytes = maxUint64 - 2
	slot := tracker.directRateRing[0]
	tracker.accountDirectPeer(
		directPacket("192.168.1.20", "192.0.2.20", true, false, 10), slot)
	tracker.accountDirectPeer(
		directPacket("192.0.2.20", "192.168.1.20", false, true, 10), slot)
	_, totals = tracker.DirectBandwidthSnapshotForModeAt(DirectViewPorts, 10, now)
	if totals.TxBytes != maxUint64 || totals.RxBytes != maxUint64 {
		t.Fatalf("overflow wrapped cumulative totals: %+v", totals)
	}
}

func TestDirectCumulativeTotalsIgnorePeerIndexCardinality(t *testing.T) {
	now := time.Unix(980, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	for i := 0; i < maxHostsPerBucket; i++ {
		slot.peers[directPairKey{
			local: "192.168.1.20", remote: fmt.Sprintf("192.0.2.%d", i),
		}] = &hostAccum{}
	}
	addedToIndex := tracker.accountDirectPeer(
		directPacket("192.168.1.20", "198.51.100.20", true, false, 77), slot)
	if addedToIndex {
		t.Fatal("peer index exceeded its cardinality bound")
	}
	if tracker.directTxBytes != 77 || tracker.directRxBytes != 0 {
		t.Fatalf("cardinality cap suppressed cumulative accounting: tx=%d rx=%d",
			tracker.directTxBytes, tracker.directRxBytes)
	}
}

func (t *Tracker) DirectBandwidthSnapshotForModeAt(mode DirectViewMode, n int, now time.Time) ([]DirectTalkerStat, DirectRateTotals) {
	if mode == DirectViewPorts {
		return t.directPortBandwidthSnapshot(n, now)
	}
	return t.directBandwidthSnapshot(n, now)
}

func TestDirectPortCardinalityBoundAndStaleEviction(t *testing.T) {
	now := time.Unix(1000, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	for port := 0; port < maxDirectFlowsPerSlot+100; port++ {
		accountDirectTestFlow(tracker, flowPacket(
			"192.168.1.20", "192.0.2.20", true, false, 1,
			"TCP", 50000, uint16(port), true,
		), slot)
	}
	if len(slot.flows) != maxDirectFlowsPerSlot {
		t.Fatalf("flow cardinality=%d, want %d", len(slot.flows), maxDirectFlowsPerSlot)
	}
	old := &directRateSlot{
		timestamp: now.Add(-41 * time.Second),
		peers:     map[directPairKey]*hostAccum{},
		flows: map[directFlowKey]*hostAccum{
			{directPairKey: directPairKey{local: "192.168.1.30", remote: "203.0.113.1"}, protocol: "TCP", remotePort: 22, hasPort: true}: {bytes: 999, txBytes: 999},
		},
	}
	tracker.directRateRing[1] = old
	rates, _ := tracker.directFlowRateFromRing(now, 40*time.Second)
	if len(rates) != maxDirectFlowsPerSlot {
		t.Fatalf("stale flow survived or current flows lost: %d", len(rates))
	}
}

func TestDirectPortRollingWindowsTopNAndDeterministicTies(t *testing.T) {
	now := time.Unix(1100, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	flow := directFlowKey{
		directPairKey: directPairKey{local: "192.168.1.20", remote: "192.0.2.20"},
		protocol:      "TCP", remotePort: 443, hasPort: true,
	}
	tracker.directRateRing[0].peers[flow.directPairKey] = &hostAccum{bytes: 300, rxBytes: 100, txBytes: 200}
	tracker.directRateRing[0].flows[flow] = &hostAccum{bytes: 300, rxBytes: 100, txBytes: 200}
	tracker.directRateRing[1] = &directRateSlot{
		timestamp: now.Add(-10 * time.Second),
		peers:     map[directPairKey]*hostAccum{flow.directPairKey: {bytes: 700, rxBytes: 300, txBytes: 400}},
		flows:     map[directFlowKey]*hostAccum{flow: {bytes: 700, rxBytes: 300, txBytes: 400}},
	}
	tracker.directRateRing[2] = &directRateSlot{
		timestamp: now.Add(-40 * time.Second),
		peers:     map[directPairKey]*hostAccum{flow.directPairKey: {bytes: 3000, rxBytes: 1000, txBytes: 2000}},
		flows:     map[directFlowKey]*hostAccum{flow: {bytes: 3000, rxBytes: 1000, txBytes: 2000}},
	}
	got, totals := tracker.directPortBandwidthSnapshot(1, now)
	if len(got) != 1 || got[0].RxRate != 50 || got[0].TxRate != 100 ||
		got[0].RxRate10 != 40 || got[0].TxRate10 != 60 ||
		got[0].RxRate40 != 35 || got[0].TxRate40 != 65 ||
		totals.RxRate != got[0].RxRate || totals.TxRate40 != got[0].TxRate40 {
		t.Fatalf("unexpected port windows or totals: rows=%+v totals=%+v", got, totals)
	}

	tieTracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tieTracker.directRateRing[0]
	accountDirectTestFlow(tieTracker, flowPacket("192.168.1.20", "198.51.100.2", true, false, 300, "UDP", 50000, 53, true), slot)
	accountDirectTestFlow(tieTracker, flowPacket("192.168.1.20", "198.51.100.1", true, false, 300, "TCP", 50001, 53, true), slot)
	accountDirectTestFlow(tieTracker, flowPacket("192.168.1.20", "203.0.113.3", true, false, 100, "TCP", 50002, 22, true), slot)
	ranked, allTotals := tieTracker.directPortBandwidthSnapshot(2, now)
	if len(ranked) != 2 || ranked[0].IP != "198.51.100.1" || ranked[1].IP != "198.51.100.2" {
		t.Fatalf("nondeterministic port tie order: %+v", ranked)
	}
	if allTotals.TxRate <= ranked[0].TxRate+ranked[1].TxRate {
		t.Fatalf("top-N totals excluded hidden rows: rows=%+v totals=%+v", ranked, allTotals)
	}
}

func accountDirectTestFlow(tracker *Tracker, packet *parsedPkt, slot *directRateSlot) {
	if tracker.accountDirectPeer(packet, slot) {
		tracker.accountDirectFlow(packet, slot)
	}
}

func flowPacket(src, dst string, srcLocal, dstLocal bool, wireLen uint64, protocol string, srcPort, dstPort uint16, hasPorts bool) *parsedPkt {
	packet := directPacket(src, dst, srcLocal, dstLocal, wireLen)
	packet.transportProtocol = protocol
	packet.srcPort = srcPort
	packet.dstPort = dstPort
	packet.hasPorts = hasPorts
	return packet
}

func assertDirectFlowStat(t *testing.T, got DirectTalkerStat, protocol string, port uint16, hasPort bool, rx, tx, packets uint64) {
	t.Helper()
	if got.Protocol != protocol || got.RemotePort != port || got.HasPort != hasPort ||
		got.RxBytes != rx || got.TxBytes != tx || got.Packets != packets {
		t.Fatalf("got %+v, want protocol=%s port=%d hasPort=%v rx=%d tx=%d packets=%d",
			got, protocol, port, hasPort, rx, tx, packets)
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
		flows:     make(map[directFlowKey]*hostAccum),
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
