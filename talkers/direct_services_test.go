package talkers

import (
	"testing"
	"time"
)

func servicePacket(src, dst string, srcLocal, dstLocal bool, wireLen uint64, service serviceID) *parsedPkt {
	packet := directPacket(src, dst, srcLocal, dstLocal, wireLen)
	packet.service = service
	return packet
}

func TestDirectServiceAggregationGroupsByServiceWithDirection(t *testing.T) {
	now := time.Unix(1100, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	for _, packet := range []*parsedPkt{
		servicePacket("192.168.1.20", "192.0.2.20", true, false, 100, svcHTTPS),
		servicePacket("192.0.2.20", "192.168.1.20", false, true, 400, svcHTTPS),
		servicePacket("192.168.1.30", "192.0.2.30", true, false, 60, svcHTTPS),
		servicePacket("192.168.1.20", "198.51.100.53", true, false, 40, svcDNS),
		servicePacket("198.51.100.53", "192.168.1.20", false, true, 80, svcDNS),
		servicePacket("192.0.2.40", "192.168.1.20", false, true, 1000, svcQUIC),
	} {
		accountDirectTestFlow(tracker, packet, slot)
	}

	got, totals := tracker.DirectBandwidthSnapshotForModeAt(DirectViewServices, 10, now)
	if len(got) != 3 {
		t.Fatalf("got %d service rows: %+v", len(got), got)
	}
	want := []struct {
		service     string
		rx, tx, pkt uint64
	}{
		{"QUIC", 1000, 0, 1},
		{"HTTPS", 400, 160, 3},
		{"DNS", 80, 40, 2},
	}
	for i, w := range want {
		row := got[i]
		if row.Service != w.service || row.RxBytes != w.rx || row.TxBytes != w.tx ||
			row.TotalBytes != w.rx+w.tx || row.Packets != w.pkt || row.IP != "" || row.LocalIP != "" {
			t.Fatalf("row %d = %+v, want %+v", i, row, w)
		}
		if row.RxRate != float64(w.rx)/2 || row.TxRate != float64(w.tx)/2 {
			t.Fatalf("row %d rates = rx %v tx %v", i, row.RxRate, row.TxRate)
		}
	}
	_, hostTotals := tracker.DirectBandwidthSnapshotForModeAt(DirectViewHosts, 10, now)
	if totals != hostTotals {
		t.Fatalf("service totals %+v differ from host totals %+v", totals, hostTotals)
	}
}

func TestDirectServiceTotalsOutliveRateWindow(t *testing.T) {
	now := time.Unix(1200, 0)
	tracker := directAggregationTracker(now.Add(-60 * time.Second))
	accountDirectTestFlow(tracker,
		servicePacket("192.168.1.20", "192.0.2.22", true, false, 300, svcSSH),
		tracker.directRateRing[0])

	got, _ := tracker.DirectBandwidthSnapshotForModeAt(DirectViewServices, 10, now)
	if len(got) != 1 || got[0].Service != "SSH" || got[0].TxBytes != 300 {
		t.Fatalf("got %+v", got)
	}
	if got[0].TxRate != 0 || got[0].TxRate40 != 0 {
		t.Fatalf("stale slot still counted in rates: %+v", got[0])
	}
}

func TestDirectServiceExcludesAmbiguousPacketsAndRespectsLimit(t *testing.T) {
	now := time.Unix(1300, 0)
	tracker := directAggregationTracker(now.Add(-2 * time.Second))
	slot := tracker.directRateRing[0]
	tracker.accountDirectPeer(servicePacket("192.168.1.20", "192.168.1.30", true, true, 999, svcSSH), slot)
	tracker.accountDirectPeer(servicePacket("192.0.2.1", "192.0.2.2", false, false, 999, svcSSH), slot)
	accountDirectTestFlow(tracker, servicePacket("192.168.1.20", "192.0.2.20", true, false, 10, svcNTP), slot)
	accountDirectTestFlow(tracker, servicePacket("192.168.1.20", "192.0.2.20", true, false, 20, svcHTTP), slot)

	got, _ := tracker.DirectBandwidthSnapshotForModeAt(DirectViewServices, 1, now)
	if len(got) != 1 || got[0].Service != "HTTP" {
		t.Fatalf("got %+v", got)
	}
	all, _ := tracker.DirectBandwidthSnapshotForModeAt(DirectViewServices, 10, now)
	for _, row := range all {
		if row.Service == "SSH" {
			t.Fatalf("ambiguous packet counted: %+v", all)
		}
	}
}

func TestDirectServiceAccountingDoesNotAllocate(t *testing.T) {
	tracker := directAggregationTracker(time.Unix(1400, 0))
	slot := tracker.directRateRing[0]
	packet := servicePacket("192.168.1.20", "192.0.2.20", true, false, 100, svcHTTPS)
	accountDirectTestFlow(tracker, packet, slot)
	if n := testing.AllocsPerRun(1000, func() { tracker.accountDirectPeer(packet, slot) }); n != 0 {
		t.Fatalf("accountDirectPeer allocates %v per packet", n)
	}
}
