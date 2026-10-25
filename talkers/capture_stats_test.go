package talkers

import (
	"testing"
	"time"

	"bandwidth-monitor/packets"
)

func TestCaptureStatsPollerAccumulates(t *testing.T) {
	tr := &Tracker{}
	reads := 0
	p := captureStatsPoller{
		device: "eth1",
		read: func() (packets.RingStats, error) {
			reads++
			return packets.RingStats{Packets: 100, Drops: 3}, nil
		},
		record: tr.recordCaptureStats,
	}
	now := time.Unix(1000, 0)
	p.poll(now, false)
	p.poll(now.Add(500*time.Millisecond), false) // throttled
	p.poll(now.Add(time.Second), false)
	p.poll(now.Add(1100*time.Millisecond), true) // final read on exit
	if reads != 3 {
		t.Fatalf("reads = %d, want 3", reads)
	}
	got := tr.CaptureStats()["eth1"]
	if got.Packets != 300 || got.Drops != 9 {
		t.Fatalf("stats = %+v, want 300 packets / 9 drops", got)
	}
}

func TestCaptureStatsPollerStopsWhenUnsupported(t *testing.T) {
	reads := 0
	p := captureStatsPoller{
		read: func() (packets.RingStats, error) {
			reads++
			return packets.RingStats{}, packets.ErrStatsUnsupported
		},
		record: func(string, packets.RingStats) { t.Fatal("unexpected record") },
	}
	p.poll(time.Unix(0, 0), true)
	p.poll(time.Unix(10, 0), true)
	if reads != 1 {
		t.Fatalf("reads = %d, want 1", reads)
	}
}
