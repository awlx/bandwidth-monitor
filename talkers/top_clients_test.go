package talkers

import (
	"net"
	"testing"
	"time"
)

func TestScopedVolumeRankingsFillRemoteAndClientLimits(t *testing.T) {
	tracker := scopedRankingTracker()
	tracker.current = rankingBucket(map[string]*hostAccum{
		"192.168.1.10": {bytes: 900, rxBytes: 600, txBytes: 300},
		"192.168.1.20": {bytes: 800, rxBytes: 500, txBytes: 300},
		"192.168.1.30": {bytes: 700, rxBytes: 400, txBytes: 300},
		"198.51.100.1": {bytes: 600, rxBytes: 400, txBytes: 200},
		"198.51.100.2": {bytes: 500, rxBytes: 300, txBytes: 200},
		"198.51.100.3": {bytes: 400, rxBytes: 200, txBytes: 200},
	})

	remotes := tracker.TopRemoteByVolume(2)
	clients := tracker.TopClientsByVolume(2)
	assertRankedIPs(t, remotes, "198.51.100.1", "198.51.100.2")
	assertRankedIPs(t, clients, "192.168.1.10", "192.168.1.20")
	if remotes[0].IsLocal || !clients[0].IsLocal {
		t.Fatalf("scope flags are wrong: remotes=%+v clients=%+v", remotes, clients)
	}
}

func TestScopedBandwidthRankingsFillRemoteAndClientLimits(t *testing.T) {
	tracker := scopedRankingTracker()
	now := time.Now().Add(-5 * time.Second)
	tracker.current = rankingBucket(nil)
	tracker.rateRing[0] = &rateSlot{
		timestamp: now,
		hosts: map[string]*hostAccum{
			"192.168.1.10": {bytes: 900, rxBytes: 600, txBytes: 300},
			"192.168.1.20": {bytes: 800, rxBytes: 500, txBytes: 300},
			"192.168.1.30": {bytes: 700, rxBytes: 400, txBytes: 300},
			"198.51.100.1": {bytes: 600, rxBytes: 400, txBytes: 200},
			"198.51.100.2": {bytes: 500, rxBytes: 300, txBytes: 200},
			"198.51.100.3": {bytes: 400, rxBytes: 200, txBytes: 200},
		},
	}

	remotes := tracker.TopRemoteByBandwidth(2)
	clients := tracker.TopClientsByBandwidth(2)
	assertRankedIPs(t, remotes, "198.51.100.1", "198.51.100.2")
	assertRankedIPs(t, clients, "192.168.1.10", "192.168.1.20")
}

func TestClientRankingsExcludeRouterAndLoopback(t *testing.T) {
	tracker := scopedRankingTracker()
	tracker.selfIPs["192.168.1.1"] = struct{}{}
	tracker.current = rankingBucket(map[string]*hostAccum{
		"192.168.1.1":  {bytes: 1000},
		"127.0.0.1":    {bytes: 900},
		"192.168.1.10": {bytes: 800},
	})

	assertRankedIPs(t, tracker.TopClientsByVolume(10), "192.168.1.10")
}

func TestAccountDirectionAttributesLocalClientTraffic(t *testing.T) {
	tracker := scopedRankingTracker()
	current := rankingBucket(nil)
	rates := &rateSlot{hosts: make(map[string]*hostAccum)}
	packet := &parsedPkt{
		srcStr: "192.0.2.10", dstStr: "198.51.100.20",
		srcLocal: true, dstLocal: true, wireLen: 1200,
	}

	tracker.accountPacket(packet, current, rates)
	tracker.accountDirection(packet, current, rates)

	if got := current.hosts[packet.srcStr]; got.bytes != 1200 || got.txBytes != 1200 || got.rxBytes != 0 {
		t.Fatalf("source counters = %+v, want total and TX only", got)
	}
	if got := current.hosts[packet.dstStr]; got.bytes != 1200 || got.rxBytes != 1200 || got.txBytes != 0 {
		t.Fatalf("destination counters = %+v, want total and RX only", got)
	}
	if got := rates.hosts[packet.srcStr]; got.txBytes != 1200 || got.rxBytes != 0 {
		t.Fatalf("source rate counters = %+v, want TX only", got)
	}
	if got := rates.hosts[packet.dstStr]; got.rxBytes != 1200 || got.txBytes != 0 {
		t.Fatalf("destination rate counters = %+v, want RX only", got)
	}
}

func scopedRankingTracker() *Tracker {
	_, localNet, _ := net.ParseCIDR("192.168.1.0/24")
	return &Tracker{
		localNets: []*net.IPNet{localNet},
		selfIPs:   make(map[string]struct{}),
	}
}

func rankingBucket(hosts map[string]*hostAccum) *bucket {
	if hosts == nil {
		hosts = make(map[string]*hostAccum)
	}
	return &bucket{
		timestamp:  time.Now(),
		hosts:      hosts,
		protoBytes: make(map[string]uint64),
		ipVerBytes: make(map[string]uint64),
		pairs:      make(map[string]map[string]*hostAccum),
	}
}

func assertRankedIPs(t *testing.T, got []TalkerStat, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d rows (%+v), want %d", len(got), got, len(want))
	}
	for i, ip := range want {
		if got[i].IP != ip {
			t.Fatalf("row %d IP = %s, want %s (%+v)", i, got[i].IP, ip, got)
		}
	}
}
