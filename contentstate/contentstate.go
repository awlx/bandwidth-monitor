// Package contentstate builds the Live Activity content-state payload pushed to the iOS app.
// Pure and dependency-free (no collector coupling) so both the local per-instance push path and
// the standalone relay gateway — which get their raw history from different sources (the in-process
// collector vs. an HTTP fetch of another server's /api/interfaces/history) — share one
// implementation, keeping the JSON shape guaranteed identical everywhere it's produced.
package contentstate

import (
	"math"
	"time"
)

// Point mirrors one collector.HistoryPoint. JSON tags MUST match the iOS HistoryPoint exactly.
type Point struct {
	T  int64   `json:"t"`
	Rx float64 `json:"rx"`
	Tx float64 `json:"tx"`
}

// State is the Live Activity content-state. JSON tags MUST match the iOS
// BandwidthActivityAttributes.ContentState exactly, or ActivityKit silently drops the push.
type State struct {
	InterfaceName string  `json:"interfaceName"`
	RxRate        float64 `json:"rxRate"`
	TxRate        float64 `json:"txRate"`
	Points        []Point `json:"points"`
	UpdatedAt     float64 `json:"updatedAt"`
}

const (
	// Window is how far back into history to look for the sparkline.
	Window = time.Hour
	// MaxPoints caps how many points are sent, downsampled keeping peaks.
	MaxPoints = 38
)

// Build mirrors the iOS liveState(): windows history to the last hour, downsamples keeping peaks,
// and appends a synthetic "now" sample carrying the live rate so the marked latest point reflects
// the current rate rather than the (possibly several-seconds-old) tail of history.
func Build(iface string, rx, tx float64, history []Point) State {
	cutoff := time.Now().Add(-Window).UnixMilli()
	var windowed []Point
	for _, p := range history {
		if p.T >= cutoff {
			windowed = append(windowed, p)
		}
	}
	windowed = DownsamplePeaks(windowed, MaxPoints)

	pts := make([]Point, 0, len(windowed)+1)
	pts = append(pts, windowed...)
	pts = append(pts, Point{T: time.Now().UnixMilli(), Rx: rx, Tx: tx})

	return State{
		InterfaceName: iface,
		RxRate:        rx,
		TxRate:        tx,
		Points:        pts,
		UpdatedAt:     float64(time.Now().UnixMilli()) / 1000,
	}
}

// DownsamplePeaks reduces pts to ~maxPoints, keeping the highest-rate sample in each bucket so brief
// spikes survive (matches the iOS downsampledPreservingPeaks).
func DownsamplePeaks(pts []Point, maxPoints int) []Point {
	if maxPoints <= 0 || len(pts) <= maxPoints {
		return pts
	}
	bucket := float64(len(pts)) / float64(maxPoints)
	out := make([]Point, 0, maxPoints)
	start := 0
	for i := 0; i < maxPoints; i++ {
		end := len(pts)
		if i < maxPoints-1 {
			end = int(math.Round(float64(i+1) * bucket))
		}
		if start >= end {
			continue
		}
		peak := pts[start]
		for _, p := range pts[start:end] {
			if math.Max(p.Rx, p.Tx) > math.Max(peak.Rx, peak.Tx) {
				peak = p
			}
		}
		out = append(out, peak)
		start = end
	}
	return out
}
