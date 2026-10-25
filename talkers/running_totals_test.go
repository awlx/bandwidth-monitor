package talkers

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

// TestRunningTotalsMatchBuckets checks that the incrementally maintained 24h
// totals always equal a full recomputation over the retained buckets, across
// many rotations that both add and expire buckets.
func TestRunningTotalsMatchBuckets(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	tr := scopedRankingTracker()
	now := time.Now().Truncate(bucketSize)
	tr.current = rankingBucket(nil)
	tr.current.timestamp = now

	for step := 0; step < 3000; step++ {
		for h := 0; h < 20; h++ {
			ip := fmt.Sprintf("192.168.1.%d", rng.Intn(60))
			acc := tr.current.hosts[ip]
			if acc == nil {
				acc = &hostAccum{}
				tr.current.hosts[ip] = acc
			}
			n := uint64(rng.Intn(1500) + 1)
			acc.bytes += n
			acc.rxBytes += n / 2
			acc.txBytes += n - n/2
			acc.packets++
			tr.current.protoBytes[[]string{"TCP", "UDP"}[rng.Intn(2)]] += n
			tr.current.ipVerBytes["IPv4"] += n
		}
		now = now.Add(bucketSize)
		next := rankingBucket(nil)
		next.timestamp = now
		tr.rotateBucketLocked(now, next)

		if step%250 != 0 && step != 2999 {
			continue
		}
		want := map[string]hostAccum{}
		wantProto := map[string]uint64{}
		for _, b := range tr.buckets {
			for ip, acc := range b.hosts {
				w := want[ip]
				w.bytes += acc.bytes
				w.rxBytes += acc.rxBytes
				w.txBytes += acc.txBytes
				w.packets += acc.packets
				want[ip] = w
			}
			for k, v := range b.protoBytes {
				wantProto[k] += v
			}
		}
		if len(want) != len(tr.closedHosts) {
			t.Fatalf("step %d: %d running hosts, want %d", step, len(tr.closedHosts), len(want))
		}
		for ip, w := range want {
			if got := tr.closedHosts[ip]; got == nil || *got != w {
				t.Fatalf("step %d: %s = %+v, want %+v", step, ip, got, w)
			}
		}
		if got := tr.GetProtocolBreakdown(); fmt.Sprint(got) != fmt.Sprint(wantProto) {
			t.Fatalf("step %d: proto = %v, want %v", step, got, wantProto)
		}
		if got := tr.UniqueIPs(); got != len(want) {
			t.Fatalf("step %d: UniqueIPs = %d, want %d", step, got, len(want))
		}
	}
	if len(tr.buckets) > int(maxAge/bucketSize)+1 {
		t.Fatalf("retained %d buckets, expected expiry", len(tr.buckets))
	}
}
