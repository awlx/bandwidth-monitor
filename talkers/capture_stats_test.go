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

func TestCaptureHealthRatesAndStatus(t *testing.T) {
	tr := &Tracker{}
	t0 := time.Unix(1000, 0)
	tr.statsMu.Lock()
	// Baseline, then 4 one-second samples.
	tr.addCaptureSampleLocked("eth0", packets.RingStats{Packets: 999999}, t0)
	tr.addCaptureSampleLocked("eth1", packets.RingStats{}, t0)
	tr.addCaptureSampleLocked("eth2", packets.RingStats{}, t0)
	for i := 1; i <= 4; i++ {
		at := t0.Add(time.Duration(i) * time.Second)
		tr.addCaptureSampleLocked("eth0", packets.RingStats{Packets: 1000}, at)
		tr.addCaptureSampleLocked("eth1", packets.RingStats{Packets: 998, Drops: 2}, at)
		tr.addCaptureSampleLocked("eth2", packets.RingStats{Packets: 980, Drops: 20}, at)
	}
	tr.statsMu.Unlock()

	got := tr.captureHealthAt(t0.Add(4 * time.Second))
	if len(got) != 3 {
		t.Fatalf("got %d devices, want 3: %+v", len(got), got)
	}
	want := []struct {
		dev         string
		pps, dps, r float64
		status      string
	}{
		{"eth0", 1000, 0, 0, "ok"},
		{"eth1", 998, 2, 0.002, "warn"},
		{"eth2", 980, 20, 0.02, "alert"},
	}
	for i, w := range want {
		g := got[i]
		if g.Device != w.dev || g.PacketsPerSec != w.pps || g.DropsPerSec != w.dps || g.DropRatio != w.r || g.Status != w.status {
			t.Errorf("device %d = %+v, want %+v", i, g, w)
		}
	}
}

func TestCaptureHealthWindow(t *testing.T) {
	tr := &Tracker{}
	t0 := time.Unix(1000, 0)
	tr.statsMu.Lock()
	for i := 0; i <= 30; i++ {
		d := packets.RingStats{Packets: 100}
		if i <= 15 {
			d.Drops = 100 // old drops must age out of the window
		}
		tr.addCaptureSampleLocked("eth0", d, t0.Add(time.Duration(i)*time.Second))
	}
	if n := len(tr.captureRecent["eth0"]); n > 12 {
		t.Errorf("kept %d samples, want window-bounded", n)
	}
	tr.statsMu.Unlock()
	now := t0.Add(30 * time.Second)
	if g := tr.captureHealthAt(now)[0]; g.DropRatio != 0 || g.PacketsPerSec != 100 || g.Status != "ok" {
		t.Errorf("health = %+v, want 100 pps, no drops", g)
	}
	// A device with no recent samples reports zero rates, not stale ones.
	if g := tr.captureHealthAt(now.Add(time.Minute))[0]; g.PacketsPerSec != 0 || g.Status != "ok" {
		t.Errorf("stale health = %+v, want zero rates", g)
	}
	if got := (&Tracker{}).CaptureHealth(); len(got) != 0 {
		t.Errorf("unsupported tracker health = %+v, want empty", got)
	}
}
