package contentstate

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The content-state JSON keys must match the iOS BandwidthActivityAttributes.ContentState exactly,
// or ActivityKit will reject the push.
func TestContentStateJSONKeys(t *testing.T) {
	s := State{
		InterfaceName: "eth0",
		RxRate:        1,
		TxRate:        2,
		Points:        []Point{{T: 123, Rx: 4, Tx: 5}},
		UpdatedAt:     6.5,
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, key := range []string{
		`"interfaceName"`, `"rxRate"`, `"txRate"`, `"points"`, `"updatedAt"`, `"t"`, `"rx"`, `"tx"`,
	} {
		if !strings.Contains(got, key) {
			t.Errorf("content-state JSON missing key %s: %s", key, got)
		}
	}
}

// Build should append a synthetic "now" point carrying the live rate, so the marked latest point on
// the sparkline reflects the current rate rather than a possibly-stale history tail.
func TestBuildAppendsNowPoint(t *testing.T) {
	history := []Point{{T: time.Now().Add(-5 * time.Minute).UnixMilli(), Rx: 10, Tx: 20}}
	s := Build("eth0", 99, 88, history)
	if len(s.Points) != 2 {
		t.Fatalf("want 2 points (history + synthetic now), got %d", len(s.Points))
	}
	last := s.Points[len(s.Points)-1]
	if last.Rx != 99 || last.Tx != 88 {
		t.Errorf("synthetic now point = %+v, want Rx=99 Tx=88", last)
	}
}

// DownsamplePeaks must keep the highest-rate sample in each bucket, not silently drop a spike
// between sampled indices.
func TestDownsamplePeaksKeepsSpikes(t *testing.T) {
	pts := make([]Point, 20)
	for i := range pts {
		pts[i] = Point{T: int64(i), Rx: 1, Tx: 1}
	}
	pts[7] = Point{T: 7, Rx: 500, Tx: 0} // a spike buried in the middle of a bucket

	out := DownsamplePeaks(pts, 5)
	found := false
	for _, p := range out {
		if p.Rx == 500 {
			found = true
		}
	}
	if !found {
		t.Errorf("downsampled output dropped the spike: %+v", out)
	}
}
