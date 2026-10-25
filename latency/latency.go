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
	"bandwidth-monitor/netutil"
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
	Name string `json:"name"`
	// WAN is the interface the probes were bound to; empty for the
	// default route. Per-WAN entries follow the default-route entry of the
	// same target.
	WAN string `json:"wan,omitempty"`
	// Unroutable lists the address families ("ipv4", "ipv6") that host
	// traffic cannot send via WAN (no default route, or the WAN's policy
	// table is not selected by any usable ip rule). They are not probed.
	Unroutable []string `json:"unroutable,omitempty"`
	// Egress describes how probes leave via WAN per family, e.g.
	// "fwmark 0x6"; omitted for plain interface binding.
	Egress  map[string]string `json:"egress,omitempty"`
	IPv4    string            `json:"ipv4,omitempty"`
	IPv6    string            `json:"ipv6,omitempty"`
	Alive   bool              `json:"alive"`
	RTT     float64           `json:"rtt_ms"`
	AvgRTT  float64           `json:"avg_rtt_ms"`
	MinRTT  float64           `json:"min_rtt_ms"`
	MaxRTT  float64           `json:"max_rtt_ms"`
	Jitter  float64           `json:"jitter_ms"`
	LossPct float64           `json:"loss_pct"`
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
	paths   []*probePath // [0] is the default route
	mu      sync.RWMutex
	poller.Runner

	newPinger func(v6 bool, eg netutil.Egress) (pinger, error)
	// resolveEgress looks up how to reach a WAN's default route under the
	// host's policy routing (see netutil.ResolveEgress).
	resolveEgress func(iface string, v6 bool) (netutil.Egress, error)

	// wanSource, when set, returns the current WAN interfaces. It is
	// polled every round and probe paths are added/removed to match.
	wanSource func() []string
	wanLogged bool

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

func defaultNewPinger(v6 bool, eg netutil.Egress) (pinger, error) {
	p, err := icmputil.NewPingerEgress(v6, eg)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// probePath is one egress path: the default route (wan == "") or a WAN
// interface the sockets are bound to.
type probePath struct {
	wan   string
	state map[string]*targetState

	// ICMP pingers are long-lived so in-flight replies are never lost to a
	// socket being closed, and are opened lazily (retried each round).
	ping4, ping6       pinger
	v4Logged, v6Logged bool

	// eg is the egress per family ([0] IPv4, [1] IPv6), re-resolved every
	// egressRecheck. gen is bumped on every change so results of probes
	// that were in flight across the change are discarded. Written by the
	// probe loop under Monitor.mu.
	eg        [2]netutil.Egress
	gen       [2]uint64
	egAt      time.Time
	egErrLogd [2]bool
}

const egressRecheck = 10 * time.Second

var familyNames = [2]string{"ipv4", "ipv6"}

func (p *probePath) unroutable(f int) bool { return p.eg[f].Unroutable != "" }

func (p *probePath) label() string {
	if p.wan == "" {
		return "default route"
	}
	return p.wan
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
// When iface is set the connection is bound to that interface.
func newDirectHTTPSClient(ip net.IP, hostname string, eg netutil.Egress) *http.Client {
	network := "tcp4"
	ipStr := ip.String()
	if ip.To4() == nil {
		network = "tcp6"
		ipStr = "[" + ipStr + "]"
	}
	dialer := netutil.DialerForEgress(eg)
	dialer.Timeout = httpsTimeout
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
// Targets are probed via the default route; see SetWANSource for per-WAN
// probing.
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

	return newMonitor(resolved, defaultNewPinger)
}

func newMonitor(targets []resolvedTarget, newPinger func(bool, netutil.Egress) (pinger, error)) *Monitor {
	m := &Monitor{targets: targets, newPinger: newPinger, resolveEgress: netutil.ResolveEgress}
	m.Runner.Init()
	m.paths = []*probePath{m.newPath("")}
	return m
}

func (m *Monitor) newPath(wan string) *probePath {
	p := &probePath{wan: wan, state: make(map[string]*targetState, len(m.targets))}
	for _, t := range m.targets {
		p.state[t.name] = &targetState{}
	}
	if wan != "" {
		for f := range p.eg {
			p.eg[f] = m.resolve(p, f)
			m.logEgress(p, f)
		}
		p.egAt = time.Now()
	}
	for f := range p.eg {
		m.buildHTTPClients(p, f)
	}
	return p
}

// resolve returns the egress for family f of a WAN path. If the routing
// state cannot be read, it falls back to plain interface binding.
func (m *Monitor) resolve(p *probePath, f int) netutil.Egress {
	eg, err := m.resolveEgress(p.wan, f == 1)
	if err != nil {
		if !p.egErrLogd[f] {
			log.Printf("latency: cannot read %s policy routing for %s, binding to the interface only: %v", familyNames[f], p.wan, err)
			p.egErrLogd[f] = true
		}
		return netutil.Egress{Iface: p.wan}
	}
	p.egErrLogd[f] = false
	return eg
}

func (m *Monitor) logEgress(p *probePath, f int) {
	if p.unroutable(f) {
		log.Printf("latency: %s via %s not routable for host traffic (%s); not probing it", familyNames[f], p.wan, p.eg[f].Unroutable)
	} else {
		log.Printf("latency: %s probes via %s: %s", familyNames[f], p.wan, p.eg[f])
	}
}

// buildHTTPClients (re)creates the HTTPS clients of family f. Callers
// other than newPath must hold m.mu.
func (m *Monitor) buildHTTPClients(p *probePath, f int) {
	for _, t := range m.targets {
		st := p.state[t.name]
		ip := t.ipv4
		if f == 1 {
			ip = t.ipv6
		}
		var c *http.Client
		if ip != nil && !p.unroutable(f) {
			c = newDirectHTTPSClient(ip, t.name, p.eg[f])
		}
		if f == 0 {
			st.httpC4 = c
		} else {
			st.httpC6 = c
		}
	}
}

// refreshEgress re-resolves the egress of WAN paths so changes to routes
// or ip rules (WAN up/down, policy changes) are followed. On a change the
// family's sockets and HTTPS clients are rebuilt and its history is reset,
// since it no longer describes the same path. Only called from the probe
// loop goroutine.
func (m *Monitor) refreshEgress(paths []*probePath) {
	now := time.Now()
	for _, p := range paths {
		if p.wan == "" || now.Sub(p.egAt) < egressRecheck {
			continue
		}
		p.egAt = now
		for f := range p.eg {
			eg := m.resolve(p, f)
			if eg.Equal(p.eg[f]) {
				continue
			}
			m.mu.Lock()
			p.eg[f] = eg
			p.gen[f]++
			m.buildHTTPClients(p, f)
			for _, st := range p.state {
				if f == 0 {
					st.icmpV4Hist, st.httpsV4Hist = nil, nil
				} else {
					st.icmpV6Hist, st.httpsV6Hist = nil, nil
				}
			}
			m.mu.Unlock()
			if f == 0 && p.ping4 != nil {
				p.ping4.Close()
				p.ping4, p.v4Logged = nil, false
			} else if f == 1 && p.ping6 != nil {
				p.ping6.Close()
				p.ping6, p.v6Logged = nil, false
			}
			m.logEgress(p, f)
		}
	}
}

// SetWANSource enables per-WAN probing. src returns the current WAN
// interface names (e.g. the collector's auto-detected or configured WAN
// set) and is polled every probe round. When it reports two or more
// interfaces, every target is additionally probed with sockets bound to
// each of them; with fewer, only the default route is probed. On Linux the
// host's policy routing is followed: if a WAN's default route lives in a
// table selected by an fwmark or source ip rule, probes carry that mark or
// source address; if no rule selects it, the WAN is reported unroutable
// for that address family instead of being probed. Must be
// called before Run. Ignored on platforms that cannot bind sockets to an
// interface.
func (m *Monitor) SetWANSource(src func() []string) {
	if !netutil.BindSupported {
		log.Printf("latency: per-WAN probes unsupported on this platform; probing via default route only")
		return
	}
	m.wanSource = src
}

// syncWANs reconciles the probe paths with the current WAN set. History
// of WANs that remain is kept; removed WANs are dropped and their sockets
// closed. Only called from the probe loop goroutine.
func (m *Monitor) syncWANs() {
	if m.wanSource == nil {
		return
	}
	var want []string
	seen := map[string]bool{}
	for _, w := range m.wanSource() {
		if w != "" && !seen[w] {
			seen[w] = true
			want = append(want, w)
		}
	}
	if len(want) < 2 {
		want = nil
	}
	sort.Strings(want)

	cur := m.paths[1:]
	if len(cur) == len(want) {
		same := true
		for i, p := range cur {
			if p.wan != want[i] {
				same = false
				break
			}
		}
		if same {
			return
		}
	}

	byWAN := make(map[string]*probePath, len(cur))
	for _, p := range cur {
		byWAN[p.wan] = p
	}
	next := []*probePath{m.paths[0]}
	for _, w := range want {
		if p := byWAN[w]; p != nil {
			next = append(next, p)
			delete(byWAN, w)
		} else {
			next = append(next, m.newPath(w))
		}
	}
	if len(want) > 0 {
		log.Printf("latency: probing via WAN interfaces %v (plus default route)", want)
	} else if len(cur) > 0 {
		log.Printf("latency: fewer than two WAN interfaces; probing via default route only")
	}

	m.mu.Lock()
	m.paths = next
	m.mu.Unlock()
	for _, p := range byWAN {
		p.close()
	}
}

func (p *probePath) close() {
	if p.ping4 != nil {
		p.ping4.Close()
		p.ping4 = nil
	}
	if p.ping6 != nil {
		p.ping6.Close()
		p.ping6 = nil
	}
}

// snapshotPaths returns the current probe paths.
func (m *Monitor) snapshotPaths() []*probePath {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]*probePath(nil), m.paths...)
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
	for _, p := range m.snapshotPaths() {
		p.close()
	}
}

// GetStatus returns the current status of all targets. For each target the
// default-route entry comes first, followed by one entry per WAN interface.
func (m *Monitor) GetStatus() []TargetStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]TargetStatus, 0, len(m.targets)*len(m.paths))
	for _, t := range m.targets {
		for _, p := range m.paths {
			ts := buildStatus(t, p.wan, p.state[t.name])
			for f, ip := range [2]net.IP{t.ipv4, t.ipv6} {
				if ip == nil {
					continue
				}
				if p.unroutable(f) {
					ts.Unroutable = append(ts.Unroutable, familyNames[f])
				} else if p.eg[f].Mark != 0 || p.eg[f].Src != nil {
					if ts.Egress == nil {
						ts.Egress = map[string]string{}
					}
					ts.Egress[familyNames[f]] = p.eg[f].String()
				}
			}
			result = append(result, ts)
		}
	}
	return result
}

func buildStatus(t resolvedTarget, wan string, st *targetState) TargetStatus {
	{
		ts := TargetStatus{Name: t.name, WAN: wan}
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
		return ts
	}
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
	m.syncWANs()
	paths := m.snapshotPaths()
	m.refreshEgress(paths)
	m.ensurePingers(paths)
	m.probeICMP(paths)
	m.startHTTPS(paths)
}

func (m *Monitor) ensurePingers(paths []*probePath) {
	for _, p := range paths {
		if p.ping4 == nil && !p.unroutable(0) {
			if pg, err := m.newPinger(false, p.eg[0]); err != nil {
				if !p.v4Logged {
					log.Printf("latency: ICMPv4 unavailable via %s (need root/CAP_NET_RAW): %v", p.label(), err)
					p.v4Logged = true
				}
			} else {
				p.ping4 = pg
			}
		}
		if p.ping6 == nil && !p.unroutable(1) {
			if pg, err := m.newPinger(true, p.eg[1]); err != nil {
				if !p.v6Logged {
					log.Printf("latency: ICMPv6 unavailable via %s: %v", p.label(), err)
					p.v6Logged = true
				}
			} else {
				p.ping6 = pg
			}
		}
	}
}

// probeResults holds one round's RTTs, indexed [path][target].
type probeResults struct{ v4, v6 [][]float64 }

func (m *Monitor) newResults(n int) probeResults {
	r := probeResults{v4: make([][]float64, n), v6: make([][]float64, n)}
	for i := 0; i < n; i++ {
		r.v4[i] = make([]float64, len(m.targets))
		r.v6[i] = make([]float64, len(m.targets))
		for j := range m.targets {
			r.v4[i][j], r.v6[i][j] = -1, -1
		}
	}
	return r
}

func (m *Monitor) probeICMP(paths []*probePath) {
	ts := time.Now().UnixMilli()
	r := m.newResults(len(paths))
	var wg sync.WaitGroup
	for pi, p := range paths {
		for ti, t := range m.targets {
			if p.ping4 != nil && t.ipv4 != nil {
				wg.Add(1)
				go func(pg pinger, out *float64, ip net.IP) {
					defer wg.Done()
					*out = pg.Ping(ip, icmpTimeout)
				}(p.ping4, &r.v4[pi][ti], t.ipv4)
			}
			if p.ping6 != nil && t.ipv6 != nil {
				wg.Add(1)
				go func(pg pinger, out *float64, ip net.IP) {
					defer wg.Done()
					*out = pg.Ping(ip, icmpTimeout)
				}(p.ping6, &r.v6[pi][ti], t.ipv6)
			}
		}
	}
	wg.Wait()
	m.record(paths, nil, ts, r, false)
}

// record appends one round of results to the ICMP or HTTPS histories.
// Unroutable families are skipped, as are families whose egress changed
// since gens (per path, taken when the probes started; nil = unchanged).
func (m *Monitor) record(paths []*probePath, gens [][2]uint64, ts int64, r probeResults, https bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for pi, p := range paths {
		var skip [2]bool
		for f := range skip {
			skip[f] = p.unroutable(f) || (gens != nil && gens[pi][f] != p.gen[f])
		}
		for ti, t := range m.targets {
			st := p.state[t.name]
			v4, v6 := &st.icmpV4Hist, &st.icmpV6Hist
			if https {
				v4, v6 = &st.httpsV4Hist, &st.httpsV6Hist
			}
			if t.ipv4 != nil && !skip[0] {
				*v4 = appendAndTrim(*v4, Point{Timestamp: ts, RTT: r.v4[pi][ti]})
			}
			if t.ipv6 != nil && !skip[1] {
				*v6 = appendAndTrim(*v6, Point{Timestamp: ts, RTT: r.v6[pi][ti]})
			}
		}
	}
}

// startHTTPS launches an HTTPS probe round in the background unless the
// previous one is still running.
func (m *Monitor) startHTTPS(paths []*probePath) {
	if !m.httpsBusy.CompareAndSwap(false, true) {
		return
	}
	m.httpsWG.Add(1)
	go func() {
		defer m.httpsWG.Done()
		defer m.httpsBusy.Store(false)
		m.probeHTTPSAll(paths)
	}()
}

func (m *Monitor) probeHTTPSAll(paths []*probePath) {
	ts := time.Now().UnixMilli()
	r := m.newResults(len(paths))
	// Clients are swapped by refreshEgress, so read them under the lock.
	gens := make([][2]uint64, len(paths))
	clients := make([][][2]*http.Client, len(paths))
	m.mu.RLock()
	for pi, p := range paths {
		gens[pi] = p.gen
		clients[pi] = make([][2]*http.Client, len(m.targets))
		for ti, t := range m.targets {
			st := p.state[t.name]
			clients[pi][ti] = [2]*http.Client{st.httpC4, st.httpC6}
		}
	}
	m.mu.RUnlock()
	var wg sync.WaitGroup
	for pi := range paths {
		for ti, t := range m.targets {
			for f, out := range [2]*float64{&r.v4[pi][ti], &r.v6[pi][ti]} {
				c := clients[pi][ti][f]
				if c == nil {
					continue
				}
				wg.Add(1)
				go func(c *http.Client, out *float64, name string) {
					defer wg.Done()
					*out = m.probeHTTPS(c, name)
				}(c, out, t.name)
			}
		}
	}
	wg.Wait()
	m.record(paths, gens, ts, r, true)
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
