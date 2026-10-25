// Package latency provides continuous ICMP and HTTPS latency monitoring
// against configurable targets, storing a rolling history of RTT measurements.
// Targets with both IPv4 and IPv6 addresses are probed on both protocols.
package latency

import (
	"context"
	"crypto/tls"
	"log"
	"math"
	"net"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"bandwidth-monitor/httputil"
	"bandwidth-monitor/icmputil"
	"bandwidth-monitor/poller"
)

const (
	probeInterval = 2 * time.Second
	icmpTimeout   = 900 * time.Millisecond
	httpsTimeout  = 3 * time.Second
	maxHistory    = 450 // 15 minutes at 2s interval
)

// DefaultTargets are used when no LATENCY_TARGETS env var is set.
// All defaults are European-operated, privacy-friendly services.
var DefaultTargets = []string{
	"anycast01.ffmuc.net",
	"anycast02.ffmuc.net",
	"dns.quad9.net",
	"dns3.digitalcourage.de",
}

// Point is a single latency measurement.
type Point struct {
	Timestamp int64   `json:"t"`
	RTT       float64 `json:"rtt"` // milliseconds, -1 = timeout/loss
}

// TargetStatus holds the current state of a monitored target.
type TargetStatus struct {
	Name    string  `json:"name"`
	IPv4    string  `json:"ipv4,omitempty"`
	IPv6    string  `json:"ipv6,omitempty"`
	Alive   bool    `json:"alive"`
	RTT     float64 `json:"rtt_ms"`
	AvgRTT  float64 `json:"avg_rtt_ms"`
	MinRTT  float64 `json:"min_rtt_ms"`
	MaxRTT  float64 `json:"max_rtt_ms"`
	Jitter  float64 `json:"jitter_ms"`
	LossPct float64 `json:"loss_pct"`
	// Per-protocol summary stats
	ICMPStats  *ProbeStats `json:"icmp_stats,omitempty"`
	HTTPSStats *ProbeStats `json:"https_stats,omitempty"`
	ICMPv4     []Point     `json:"icmp_v4,omitempty"`
	ICMPv6     []Point     `json:"icmp_v6,omitempty"`
	HTTPSv4    []Point     `json:"https_v4,omitempty"`
	HTTPSv6    []Point     `json:"https_v6,omitempty"`
	// Legacy fields (preferred stack) for backward compat
	ICMP  []Point `json:"icmp"`
	HTTPS []Point `json:"https"`
}

// ProbeStats holds summary statistics for a single probe type.
type ProbeStats struct {
	RTT     float64 `json:"rtt_ms"`
	AvgRTT  float64 `json:"avg_rtt_ms"`
	MinRTT  float64 `json:"min_rtt_ms"`
	MaxRTT  float64 `json:"max_rtt_ms"`
	P95RTT  float64 `json:"p95_rtt_ms"`
	P99RTT  float64 `json:"p99_rtt_ms"`
	Jitter  float64 `json:"jitter_ms"`
	LossPct float64 `json:"loss_pct"`
}

// Monitor continuously probes a set of targets via ICMP and HTTPS.
type Monitor struct {
	targets []resolvedTarget
	mu      sync.RWMutex
	state   map[string]*targetState
	poller.Runner

	// ICMP pingers are long-lived so in-flight replies are never lost to a
	// socket being closed, and are opened lazily (retried each round).
	newPinger    func(v6 bool) (pinger, error)
	ping4, ping6 pinger
	icmpV4Logged bool
	icmpV6Logged bool

	// HTTPS probes run asynchronously so a slow handshake cannot stretch
	// the ICMP probe cadence.
	httpsBusy atomic.Bool
	httpsWG   sync.WaitGroup
}

// pinger is implemented by *icmputil.Pinger.
type pinger interface {
	Ping(dst net.IP, timeout time.Duration) float64
	Close() error
}

func defaultNewPinger(v6 bool) (pinger, error) {
	p, err := icmputil.NewPinger(v6)
	if err != nil {
		return nil, err
	}
	return p, nil
}

type resolvedTarget struct {
	name string
	ipv4 net.IP // nil if no v4
	ipv6 net.IP // nil if no v6
}

type targetState struct {
	icmpV4Hist  []Point
	icmpV6Hist  []Point
	httpsV4Hist []Point
	httpsV6Hist []Point
	httpC4      *http.Client // direct-IP HTTPS client for IPv4 (nil if no v4)
	httpC6      *http.Client // direct-IP HTTPS client for IPv6 (nil if no v6)
}

// newDirectHTTPSClient returns an HTTP client that connects directly to the
// given IP address, bypassing DNS resolution entirely.  The TLS ServerName
// is set to hostname so certificate validation works correctly.
func newDirectHTTPSClient(ip net.IP, hostname string) *http.Client {
	network := "tcp4"
	ipStr := ip.String()
	if ip.To4() == nil {
		network = "tcp6"
		ipStr = "[" + ipStr + "]"
	}
	dialer := &net.Dialer{Timeout: httpsTimeout}
	return &http.Client{
		Timeout: httpsTimeout,
		Transport: httputil.WrapTransport(&http.Transport{
			TLSClientConfig: &tls.Config{
				ServerName: hostname,
			},
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				// Replace the hostname in addr with the resolved IP.
				_, port, _ := net.SplitHostPort(addr)
				return dialer.DialContext(ctx, network, ipStr+":"+port)
			},
		}),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// New creates a latency monitor. Pass nil or empty targets to use defaults.
func New(targets []string) *Monitor {
	if len(targets) == 0 {
		targets = DefaultTargets
	}

	var resolved []resolvedTarget
	for _, t := range targets {
		rt := resolvedTarget{name: t}
		ips, err := net.LookupIP(t)
		if err != nil || len(ips) == 0 {
			ip := net.ParseIP(t)
			if ip == nil {
				log.Printf("latency: cannot resolve %q, skipping", t)
				continue
			}
			if ip.To4() != nil {
				rt.ipv4 = ip.To4()
			} else {
				rt.ipv6 = ip
			}
			resolved = append(resolved, rt)
			continue
		}
		for _, ip := range ips {
			if ip.To4() != nil && rt.ipv4 == nil {
				rt.ipv4 = ip.To4()
			} else if ip.To4() == nil && rt.ipv6 == nil {
				rt.ipv6 = ip
			}
		}
		if rt.ipv4 != nil || rt.ipv6 != nil {
			resolved = append(resolved, rt)
		}
	}

	m := &Monitor{
		targets:   resolved,
		state:     make(map[string]*targetState, len(resolved)),
		newPinger: defaultNewPinger,
	}
	m.Runner.Init()
	for _, t := range resolved {
		st := &targetState{}
		if t.ipv4 != nil {
			st.httpC4 = newDirectHTTPSClient(t.ipv4, t.name)
		}
		if t.ipv6 != nil {
			st.httpC6 = newDirectHTTPSClient(t.ipv6, t.name)
		}
		m.state[t.name] = st
	}
	return m
}

// Run starts the probe loop. Call in a goroutine.
func (m *Monitor) Run() {
	if len(m.targets) == 0 {
		return
	}
	log.Printf("latency: monitoring %d target(s)", len(m.targets))
	for _, t := range m.targets {
		v4s, v6s := "—", "—"
		if t.ipv4 != nil {
			v4s = t.ipv4.String()
		}
		if t.ipv6 != nil {
			v6s = t.ipv6.String()
		}
		log.Printf("  %s (v4=%s, v6=%s)", t.name, v4s, v6s)
	}

	m.Runner.Run(probeInterval, m.probeAll)
}

// Stop terminates the probe loop, waits for in-flight probes and closes
// the ICMP sockets.
func (m *Monitor) Stop() {
	m.Runner.Stop()
	m.httpsWG.Wait()
	if m.ping4 != nil {
		m.ping4.Close()
		m.ping4 = nil
	}
	if m.ping6 != nil {
		m.ping6.Close()
		m.ping6 = nil
	}
}

// GetStatus returns the current status of all targets.
func (m *Monitor) GetStatus() []TargetStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]TargetStatus, 0, len(m.targets))
	for _, t := range m.targets {
		st := m.state[t.name]
		ts := TargetStatus{Name: t.name}
		if t.ipv4 != nil {
			ts.IPv4 = t.ipv4.String()
		}
		if t.ipv6 != nil {
			ts.IPv6 = t.ipv6.String()
		}

		// Primary stats from ICMPv4, fallback to ICMPv6
		primary := st.icmpV4Hist
		if len(primary) == 0 {
			primary = st.icmpV6Hist
		}
		if len(primary) > 0 {
			last := primary[len(primary)-1]
			ts.Alive = last.RTT >= 0
			ts.RTT = last.RTT
			ts.AvgRTT, ts.MinRTT, ts.MaxRTT, ts.Jitter = computeStats(primary)
			var lost int
			for _, p := range primary {
				if p.RTT < 0 {
					lost++
				}
			}
			ts.LossPct = float64(lost) / float64(len(primary)) * 100
		}

		ts.ICMPv4 = copyPoints(st.icmpV4Hist)
		ts.ICMPv6 = copyPoints(st.icmpV6Hist)
		ts.HTTPSv4 = copyPoints(st.httpsV4Hist)
		ts.HTTPSv6 = copyPoints(st.httpsV6Hist)

		// Per-protocol summary stats (ICMP uses v4 with v6 fallback, same for HTTPS)
		icmpHist := st.icmpV4Hist
		if len(icmpHist) == 0 {
			icmpHist = st.icmpV6Hist
		}
		if len(icmpHist) > 0 {
			ts.ICMPStats = buildProbeStats(icmpHist)
		}
		httpsHist := st.httpsV4Hist
		if len(httpsHist) == 0 {
			httpsHist = st.httpsV6Hist
		}
		if len(httpsHist) > 0 {
			ts.HTTPSStats = buildProbeStats(httpsHist)
		}

		// Legacy: prefer v4 if available
		if len(ts.ICMPv4) > 0 {
			ts.ICMP = ts.ICMPv4
		} else {
			ts.ICMP = ts.ICMPv6
		}
		if len(ts.HTTPSv4) > 0 {
			ts.HTTPS = ts.HTTPSv4
		} else {
			ts.HTTPS = ts.HTTPSv6
		}

		result = append(result, ts)
	}
	return result
}

func copyPoints(s []Point) []Point {
	if len(s) == 0 {
		return nil
	}
	c := make([]Point, len(s))
	copy(c, s)
	return c
}

func buildProbeStats(pts []Point) *ProbeStats {
	if len(pts) == 0 {
		return nil
	}
	s := &ProbeStats{}
	last := pts[len(pts)-1]
	s.RTT = last.RTT
	s.AvgRTT, s.MinRTT, s.MaxRTT, s.Jitter = computeStats(pts)
	// Percentiles from sorted good values
	var good []float64
	for _, p := range pts {
		if p.RTT >= 0 {
			good = append(good, p.RTT)
		}
	}
	if len(good) > 0 {
		sort.Float64s(good)
		s.P95RTT = good[int(float64(len(good))*0.95)%len(good)]
		s.P99RTT = good[int(float64(len(good))*0.99)%len(good)]
	}
	var lost int
	for _, p := range pts {
		if p.RTT < 0 {
			lost++
		}
	}
	s.LossPct = float64(lost) / float64(len(pts)) * 100
	return s
}

func computeStats(pts []Point) (avg, min, max, jitter float64) {
	min = math.MaxFloat64
	var sum float64
	var count int
	var prev float64
	var jitterSum float64
	hasPrev := false

	for _, p := range pts {
		if p.RTT < 0 {
			hasPrev = false
			continue
		}
		sum += p.RTT
		count++
		if p.RTT < min {
			min = p.RTT
		}
		if p.RTT > max {
			max = p.RTT
		}
		if hasPrev {
			jitterSum += math.Abs(p.RTT - prev)
		}
		prev = p.RTT
		hasPrev = true
	}
	if count == 0 {
		return 0, 0, 0, 0
	}
	avg = sum / float64(count)
	if count > 1 {
		jitter = jitterSum / float64(count-1)
	}
	return
}

// probeAll runs one probe round. ICMP probes for every target and address
// family are sent concurrently at the start of each tick so each target is
// pinged on a steady probeInterval cadence. Irregular, longer gaps (e.g.
// ~4s when serial HTTPS probes overran the tick) can coincide with
// stateful middleboxes expiring their ICMP state and dropping the reply,
// which showed up as large phantom loss.
func (m *Monitor) probeAll() {
	m.ensurePingers()
	m.probeICMP()
	m.startHTTPS()
}

func (m *Monitor) ensurePingers() {
	if m.ping4 == nil {
		p, err := m.newPinger(false)
		if err != nil {
			if !m.icmpV4Logged {
				log.Printf("latency: ICMPv4 unavailable (need root/CAP_NET_RAW): %v", err)
				m.icmpV4Logged = true
			}
		} else {
			m.ping4 = p
		}
	}
	if m.ping6 == nil {
		p, err := m.newPinger(true)
		if err != nil {
			if !m.icmpV6Logged {
				log.Printf("latency: ICMPv6 unavailable: %v", err)
				m.icmpV6Logged = true
			}
		} else {
			m.ping6 = p
		}
	}
}

func (m *Monitor) probeICMP() {
	ts := time.Now().UnixMilli()
	v4 := make([]float64, len(m.targets))
	v6 := make([]float64, len(m.targets))
	var wg sync.WaitGroup
	for i, t := range m.targets {
		v4[i], v6[i] = -1, -1
		if m.ping4 != nil && t.ipv4 != nil {
			wg.Add(1)
			go func(i int, ip net.IP) {
				defer wg.Done()
				v4[i] = m.ping4.Ping(ip, icmpTimeout)
			}(i, t.ipv4)
		}
		if m.ping6 != nil && t.ipv6 != nil {
			wg.Add(1)
			go func(i int, ip net.IP) {
				defer wg.Done()
				v6[i] = m.ping6.Ping(ip, icmpTimeout)
			}(i, t.ipv6)
		}
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.targets {
		st := m.state[t.name]
		if t.ipv4 != nil {
			st.icmpV4Hist = appendAndTrim(st.icmpV4Hist, Point{Timestamp: ts, RTT: v4[i]})
		}
		if t.ipv6 != nil {
			st.icmpV6Hist = appendAndTrim(st.icmpV6Hist, Point{Timestamp: ts, RTT: v6[i]})
		}
	}
}

// startHTTPS launches an HTTPS probe round in the background unless the
// previous one is still running.
func (m *Monitor) startHTTPS() {
	if !m.httpsBusy.CompareAndSwap(false, true) {
		return
	}
	m.httpsWG.Add(1)
	go func() {
		defer m.httpsWG.Done()
		defer m.httpsBusy.Store(false)
		m.probeHTTPSAll()
	}()
}

func (m *Monitor) probeHTTPSAll() {
	ts := time.Now().UnixMilli()
	v4 := make([]float64, len(m.targets))
	v6 := make([]float64, len(m.targets))
	var wg sync.WaitGroup
	for i, t := range m.targets {
		v4[i], v6[i] = -1, -1
		st := m.state[t.name]
		if t.ipv4 != nil {
			wg.Add(1)
			go func(i int, c *http.Client, name string) {
				defer wg.Done()
				v4[i] = m.probeHTTPS(c, name)
			}(i, st.httpC4, t.name)
		}
		if t.ipv6 != nil {
			wg.Add(1)
			go func(i int, c *http.Client, name string) {
				defer wg.Done()
				v6[i] = m.probeHTTPS(c, name)
			}(i, st.httpC6, t.name)
		}
	}
	wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()
	for i, t := range m.targets {
		st := m.state[t.name]
		if t.ipv4 != nil {
			st.httpsV4Hist = appendAndTrim(st.httpsV4Hist, Point{Timestamp: ts, RTT: v4[i]})
		}
		if t.ipv6 != nil {
			st.httpsV6Hist = appendAndTrim(st.httpsV6Hist, Point{Timestamp: ts, RTT: v6[i]})
		}
	}
}

func appendAndTrim(s []Point, p Point) []Point {
	s = append(s, p)
	if len(s) > maxHistory {
		trimmed := make([]Point, maxHistory)
		copy(trimmed, s[len(s)-maxHistory:])
		return trimmed
	}
	return s
}

func (m *Monitor) probeHTTPS(client *http.Client, hostname string) float64 {
	url := "https://" + hostname
	start := time.Now()
	resp, err := client.Get(url)
	if err != nil {
		return -1
	}
	resp.Body.Close()
	return float64(time.Since(start).Microseconds()) / 1000.0
}
