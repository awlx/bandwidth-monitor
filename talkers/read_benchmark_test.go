package talkers

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"bandwidth-monitor/geoip"
)

// populatedTracker builds a tracker with a full 24h history: one bucket per
// minute, each holding hostsPerBucket hosts drawn from a pool of poolSize IPs
// (half LAN, half remote), so readers see realistic overlap across buckets.
func populatedTracker(tb testing.TB, buckets, hostsPerBucket, poolSize int) *Tracker {
	tb.Helper()
	_, lan, _ := net.ParseCIDR("10.0.0.0/8")
	t := &Tracker{
		localNets: []*net.IPNet{lan},
		selfIPs:   map[string]struct{}{"10.0.0.1": {}},
	}
	// Set BENCH_GEO_CITY/BENCH_GEO_ASN to MMDB paths to include GeoIP work.
	if city, asn := os.Getenv("BENCH_GEO_CITY"), os.Getenv("BENCH_GEO_ASN"); city != "" || asn != "" {
		db, err := geoip.Open(city, asn)
		if err != nil {
			tb.Fatalf("geoip: %v", err)
		}
		tb.Cleanup(db.Close)
		t.geoDB = db
	}
	pool := make([]string, poolSize)
	for i := range pool {
		if i%2 == 0 {
			pool[i] = fmt.Sprintf("10.%d.%d.%d", (i>>16)&0xff, (i>>8)&0xff, i&0xff)
		} else {
			pool[i] = fmt.Sprintf("198.%d.%d.%d", (i>>16)&0xff, (i>>8)&0xff, i&0xff)
		}
	}
	start := time.Now().Add(-time.Duration(buckets) * bucketSize)
	for bi := 0; bi < buckets; bi++ {
		b := rankingBucket(nil)
		b.timestamp = start.Add(time.Duration(bi) * bucketSize)
		for h := 0; h < hostsPerBucket; h++ {
			ip := pool[(bi*7919+h*31)%poolSize]
			b.hosts[ip] = &hostAccum{bytes: uint64(1000 + h), rxBytes: uint64(h), txBytes: 1000, packets: 3}
		}
		b.protoBytes["TCP"] = 1000
		b.ipVerBytes["IPv4"] = 1000
		t.appendClosedBucket(b)
	}
	t.current = rankingBucket(nil)
	for h := 0; h < hostsPerBucket; h++ {
		t.current.hosts[pool[h]] = &hostAccum{bytes: 10, packets: 1}
	}
	t.rateRing[0] = &rateSlot{timestamp: time.Now().Add(-5 * time.Second), hosts: t.current.hosts}
	return t
}

// BenchmarkSSETalkerReads models the talker work one SSE tick performs for a
// single connected client (handler.buildPayload).
func BenchmarkSSETalkerReads(b *testing.B) {
	for _, size := range []struct{ hosts, pool int }{{200, 2000}, {1000, 10000}} {
		b.Run(fmt.Sprintf("hosts=%d/pool=%d", size.hosts, size.pool), func(b *testing.B) {
			t := populatedTracker(b, 1440, size.hosts, size.pool)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = t.GetGeoBreakdown()
				_ = t.GetProtocolBreakdown()
				_ = t.GetIPVersionBreakdown()
				_ = t.TopRemoteByBandwidth(10)
				_ = t.TopClientsByBandwidth(10)
				_ = t.TopRemoteByVolume(10)
				_ = t.TopClientsByVolume(10)
				_ = t.UniqueIPs()
			}
		})
	}
}

// BenchmarkRotateBucket measures the once-per-minute cost of closing the
// current bucket and expiring the oldest one.
func BenchmarkRotateBucket(b *testing.B) {
	t := populatedTracker(b, 1440, 1000, 10000)
	now := t.buckets[len(t.buckets)-1].timestamp.Add(bucketSize)
	template := t.buckets[0]
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		// Re-close a full bucket each minute so every rotation adds and
		// expires one populated bucket.
		next := &bucket{timestamp: now, hosts: make(map[string]*hostAccum, len(template.hosts)),
			protoBytes: template.protoBytes, ipVerBytes: template.ipVerBytes}
		for ip, acc := range template.hosts {
			c := *acc
			next.hosts[ip] = &c
		}
		now = now.Add(bucketSize)
		t.rotateBucketLocked(now, next)
	}
}

// syntheticIPv4 builds a minimal raw IPv4+TCP header as delivered by the
// SOCK_DGRAM capture ring.
func syntheticIPv4(src, dst net.IP, totalLen uint16) []byte {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	pkt[2], pkt[3] = byte(totalLen>>8), byte(totalLen)
	pkt[9] = 6
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	pkt[20], pkt[21] = 0xc0, 0x00
	pkt[22], pkt[23] = 0x01, 0xbb
	return pkt
}

// BenchmarkCapturePipeline measures per-packet capture cost: parse and
// classify outside the lock, then apply a ring block's worth under it.
func BenchmarkCapturePipeline(b *testing.B) {
	_, lan, _ := net.ParseCIDR("10.0.0.0/8")
	t := &Tracker{localNets: []*net.IPNet{lan}, selfIPs: map[string]struct{}{"10.0.0.1": {}}}
	t.current = rankingBucket(nil)
	t.rateRing[0] = &rateSlot{timestamp: time.Now(), hosts: make(map[string]*hostAccum)}
	const block = 170
	pkts := make([][]byte, 4096)
	for i := range pkts {
		local := net.IPv4(10, 0, byte(i%50), byte(2+i%7))
		remote := net.IPv4(198, 51, byte(i%250), byte(i%200))
		if i%2 == 0 {
			pkts[i] = syntheticIPv4(local, remote, 1500)
		} else {
			pkts[i] = syntheticIPv4(remote, local, 1500)
		}
	}
	cache := make(map[[16]byte]*packetEndpoint, 256)
	batch := make([]parsedPkt, 0, 256)
	i := 0
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		batch = batch[:0]
		for j := 0; j < block; j++ {
			if p, ok := t.parsePacket(pkts[i%len(pkts)], 1514, cache); ok {
				batch = append(batch, p)
			}
			i++
		}
		t.applyBatch(batch)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*block), "ns/pkt")
}
