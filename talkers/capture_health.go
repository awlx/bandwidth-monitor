package talkers

import (
	"sort"
	"time"

	"bandwidth-monitor/packets"
)

const (
	captureHealthWindow = 10 * time.Second
	// Drop-ratio thresholds, matching the Grafana capture panels.
	CaptureDropWarnRatio  = 0.001
	CaptureDropAlertRatio = 0.01
)

type captureSample struct {
	at    time.Time
	delta packets.RingStats
}

// CaptureHealth is the recent kernel capture-ring health of one device.
type CaptureHealth struct {
	Device        string  `json:"device"`
	PacketsPerSec float64 `json:"packets_per_sec"`
	DropsPerSec   float64 `json:"drops_per_sec"`
	// DropRatio is drops / (packets + drops) over the window, like the
	// Grafana drop-ratio panel.
	DropRatio float64 `json:"drop_ratio"`
	// Status is "ok", "warn" (≥0.1%) or "alert" (≥1%).
	Status string `json:"status"`
}

// addCaptureSampleLocked keeps the deltas of the last window per device.
// Caller holds statsMu.
func (t *Tracker) addCaptureSampleLocked(device string, delta packets.RingStats, now time.Time) {
	if t.captureRecent == nil {
		t.captureRecent = make(map[string][]captureSample)
	}
	s := append(t.captureRecent[device], captureSample{at: now, delta: delta})
	// Keep one sample older than the window as the rate baseline.
	cut := 0
	for cut+1 < len(s) && now.Sub(s[cut+1].at) >= captureHealthWindow {
		cut++
	}
	t.captureRecent[device] = append(s[:0], s[cut:]...)
}

// CaptureHealth returns per-device capture rates and drop ratio over the
// last ~10s, sorted by device. It is empty when the platform has no kernel
// capture counters.
func (t *Tracker) CaptureHealth() []CaptureHealth {
	return t.captureHealthAt(time.Now())
}

func (t *Tracker) captureHealthAt(now time.Time) []CaptureHealth {
	t.statsMu.Lock()
	defer t.statsMu.Unlock()
	out := make([]CaptureHealth, 0, len(t.captureRecent))
	for dev, s := range t.captureRecent {
		h := CaptureHealth{Device: dev, Status: "ok"}
		// The first sample is the baseline; its delta predates the window.
		if len(s) >= 2 && now.Sub(s[len(s)-1].at) < captureHealthWindow {
			var pk, dr uint64
			for _, x := range s[1:] {
				pk += x.delta.Packets
				dr += x.delta.Drops
			}
			if secs := s[len(s)-1].at.Sub(s[0].at).Seconds(); secs > 0 {
				h.PacketsPerSec = float64(pk) / secs
				h.DropsPerSec = float64(dr) / secs
			}
			if pk+dr > 0 {
				h.DropRatio = float64(dr) / float64(pk+dr)
			}
		}
		switch {
		case h.DropRatio >= CaptureDropAlertRatio:
			h.Status = "alert"
		case h.DropRatio >= CaptureDropWarnRatio:
			h.Status = "warn"
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}
