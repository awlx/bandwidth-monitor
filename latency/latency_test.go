package latency

import (
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"bandwidth-monitor/netutil"
)

type fakePinger struct {
	rtt    float64
	calls  atomic.Int32
	closed atomic.Bool
}

func (f *fakePinger) Ping(net.IP, time.Duration) float64 {
	f.calls.Add(1)
	return f.rtt
}

func (f *fakePinger) Close() error { f.closed.Store(true); return nil }

type slowTransport struct{ d time.Duration }

func (s slowTransport) RoundTrip(*http.Request) (*http.Response, error) {
	time.Sleep(s.d)
	return nil, errors.New("slow")
}

var testTargets = []resolvedTarget{
	{name: "a.example", ipv4: net.ParseIP("192.0.2.1").To4(), ipv6: net.ParseIP("2001:db8::1")},
	{name: "b.example", ipv4: net.ParseIP("192.0.2.2").To4()},
}

func newTestMonitor(p4, p6 pinger, httpDelay time.Duration) *Monitor {
	m := newMonitor(testTargets, func(v6 bool, _ netutil.Egress) (pinger, error) {
		if v6 {
			if p6 == nil {
				return nil, errors.New("no v6")
			}
			return p6, nil
		}
		return p4, nil
	})
	c := &http.Client{Transport: slowTransport{httpDelay}}
	for _, st := range m.paths[0].state {
		st.httpC4, st.httpC6 = c, c
	}
	return m
}

// Regression: HTTPS probes used to run serially inside the ICMP round, so
// slow handshakes stretched the per-target ICMP interval to ~4s. Some
// stateful middleboxes drop echo replies at that spacing, producing large
// phantom loss. ICMP must stay on the tick regardless of HTTPS latency.
func TestICMPNotDelayedBySlowHTTPS(t *testing.T) {
	p4, p6 := &fakePinger{rtt: 10}, &fakePinger{rtt: 12}
	m := newTestMonitor(p4, p6, 1500*time.Millisecond)

	start := time.Now()
	m.probeAll()
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("probeAll blocked for %v by HTTPS probes", el)
	}
	// Second tick while HTTPS still running: ICMP must still be recorded.
	m.probeAll()

	st := m.GetStatus()
	if got := len(st[0].ICMPv4); got != 2 {
		t.Fatalf("want 2 ICMPv4 points, got %d", got)
	}
	if got := len(st[0].ICMPv6); got != 2 {
		t.Fatalf("want 2 ICMPv6 points, got %d", got)
	}
	if st[0].LossPct != 0 || st[0].RTT != 10 {
		t.Fatalf("unexpected stats: loss=%v rtt=%v", st[0].LossPct, st[0].RTT)
	}
	if p4.calls.Load() != 4 || p6.calls.Load() != 2 {
		t.Fatalf("ping calls v4=%d v6=%d", p4.calls.Load(), p6.calls.Load())
	}

	m.Stop()
	// Overlapping HTTPS round was skipped, the first one completed.
	if got := len(m.GetStatus()[0].HTTPSv4); got != 1 {
		t.Fatalf("want 1 HTTPS point after Stop, got %d", got)
	}
	if !p4.closed.Load() || !p6.closed.Load() {
		t.Fatal("pingers not closed on Stop")
	}
}

// ICMP sockets are long-lived: opened once, reused across rounds, and
// retried when unavailable.
func TestPingerReusedAndRetried(t *testing.T) {
	p4 := &fakePinger{rtt: 5}
	m := newTestMonitor(p4, nil, 0)
	opens := 0
	inner := m.newPinger
	m.newPinger = func(v6 bool, eg netutil.Egress) (pinger, error) {
		if !v6 {
			opens++
		}
		return inner(v6, eg)
	}
	for i := 0; i < 3; i++ {
		m.probeAll()
	}
	m.Stop()
	if opens != 1 {
		t.Fatalf("v4 pinger opened %d times, want 1", opens)
	}
	st := m.GetStatus()
	if len(st[0].ICMPv6) != 3 || st[0].ICMPv6[0].RTT != -1 {
		t.Fatalf("v6 unavailable should record loss points: %+v", st[0].ICMPv6)
	}
}

// wanPinger records which interface each socket was bound to and returns a
// per-interface RTT so results can be told apart.
type wanPinger struct {
	iface  string
	rtt    float64
	closed atomic.Bool
}

func (w *wanPinger) Ping(net.IP, time.Duration) float64 { return w.rtt }
func (w *wanPinger) Close() error                       { w.closed.Store(true); return nil }

func statusKeys(st []TargetStatus) []string {
	var out []string
	for _, s := range st {
		out = append(out, s.Name+"@"+s.WAN)
	}
	return out
}

// Per-WAN probe paths follow the (auto-detected) WAN set at runtime.
func TestWANPathsFollowSource(t *testing.T) {
	rtts := map[string]float64{"": 1, "wanA": 10, "wanB": 20, "wanC": 30}
	pingers := map[string]*wanPinger{}
	m := newMonitor(testTargets, func(v6 bool, eg netutil.Egress) (pinger, error) {
		if v6 {
			return nil, errors.New("no v6")
		}
		p := &wanPinger{iface: eg.Iface, rtt: rtts[eg.Iface]}
		pingers[eg.Iface] = p
		return p, nil
	})
	m.resolveEgress = plainEgress
	var wans []string
	m.wanSource = func() []string { return wans }
	defer m.Stop()

	// No / single WAN: default route only, no wan field.
	for _, w := range [][]string{nil, {"wanA"}} {
		wans = w
		m.probeAll()
		st := m.GetStatus()
		if len(st) != 2 || st[0].WAN != "" || st[1].WAN != "" {
			t.Fatalf("wans=%v: want 2 default-route entries, got %v", w, statusKeys(st))
		}
	}

	// Two WANs (unsorted, with a duplicate): default + each WAN, per target.
	wans = []string{"wanB", "wanA", "wanB"}
	m.probeAll()
	st := m.GetStatus()
	want := []string{"a.example@", "a.example@wanA", "a.example@wanB", "b.example@", "b.example@wanA", "b.example@wanB"}
	if got := statusKeys(st); len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}
	if st[1].RTT != 10 || st[2].RTT != 20 || st[0].RTT != 1 {
		t.Fatalf("RTTs not attributed to their WAN: %v %v %v", st[0].RTT, st[1].RTT, st[2].RTT)
	}
	wanAHist := len(st[1].ICMPv4)

	// WAN set changes: wanB disappears, wanC appears. wanA history is kept,
	// wanB's socket is closed.
	wans = []string{"wanA", "wanC"}
	m.probeAll()
	st = m.GetStatus()
	if got := statusKeys(st); got[1] != "a.example@wanA" || got[2] != "a.example@wanC" || len(got) != 6 {
		t.Fatalf("after change: %v", got)
	}
	if len(st[1].ICMPv4) != wanAHist+1 {
		t.Fatalf("wanA history not kept: %d -> %d", wanAHist, len(st[1].ICMPv4))
	}
	if !pingers["wanB"].closed.Load() {
		t.Fatal("removed WAN pinger not closed")
	}

	// Back to one WAN: per-WAN paths are dropped.
	wans = []string{"wanC"}
	m.probeAll()
	if st := m.GetStatus(); len(st) != 2 {
		t.Fatalf("want default-route only, got %v", statusKeys(st))
	}
	if !pingers["wanA"].closed.Load() || !pingers["wanC"].closed.Load() {
		t.Fatal("WAN pingers not closed after dropping to one WAN")
	}
}

// A WAN whose socket cannot be opened (e.g. no CAP_NET_RAW) records loss
// for that WAN only and does not affect the other paths.
func TestWANBindFailureIsolated(t *testing.T) {
	m := newMonitor(testTargets[1:], func(v6 bool, eg netutil.Egress) (pinger, error) {
		if v6 || eg.Iface == "wanB" {
			return nil, errors.New("bind failed")
		}
		return &wanPinger{rtt: 5}, nil
	})
	m.resolveEgress = plainEgress
	m.wanSource = func() []string { return []string{"wanA", "wanB"} }
	m.probeAll()
	m.Stop()
	st := m.GetStatus()
	if len(st) != 3 || st[0].RTT != 5 || st[1].RTT != 5 || st[2].RTT != -1 {
		t.Fatalf("unexpected: %v rtts=%v,%v,%v", statusKeys(st), st[0].RTT, st[1].RTT, st[2].RTT)
	}
}

func plainEgress(iface string, _ bool) (netutil.Egress, error) {
	return netutil.Egress{Iface: iface}, nil
}

// egressPinger records the egress each socket was opened with.
type egressPinger struct {
	eg     netutil.Egress
	closed atomic.Bool
}

func (e *egressPinger) Ping(net.IP, time.Duration) float64 { return 7 }
func (e *egressPinger) Close() error                       { e.closed.Store(true); return nil }

// policyMonitor returns a two-WAN monitor (IPv4-only target) whose egress
// resolution is driven by egress[wan].
func policyMonitor(egress map[string]netutil.Egress) (*Monitor, map[string][]*egressPinger) {
	opened := map[string][]*egressPinger{}
	m := newMonitor(testTargets[1:], func(v6 bool, eg netutil.Egress) (pinger, error) {
		if v6 {
			return nil, errors.New("no v6")
		}
		p := &egressPinger{eg: eg}
		opened[eg.Iface] = append(opened[eg.Iface], p)
		return p, nil
	})
	m.resolveEgress = func(iface string, v6 bool) (netutil.Egress, error) {
		if v6 {
			return netutil.Egress{Iface: iface, Unroutable: "no default route"}, nil
		}
		if eg, ok := egress[iface]; ok {
			return eg, nil
		}
		return netutil.Egress{}, errors.New("netlink unavailable")
	}
	m.wanSource = func() []string { return []string{"wanA", "wanB"} }
	return m, opened
}

// A WAN whose policy table no ip rule selects is reported unroutable and
// not probed, instead of showing 100% loss.
func TestUnroutableWANNotProbed(t *testing.T) {
	m, opened := policyMonitor(map[string]netutil.Egress{
		"wanA": {Iface: "wanA", Mark: 0x6},
		"wanB": {Iface: "wanB", Unroutable: "no ip rule selects table 100"},
	})
	for i := 0; i < 3; i++ {
		m.probeAll()
	}
	m.Stop()
	if len(opened["wanB"]) != 0 {
		t.Fatalf("socket opened for unroutable WAN: %+v", opened["wanB"][0].eg)
	}
	if got := opened["wanA"]; len(got) != 1 || got[0].eg.Mark != 0x6 {
		t.Fatalf("wanA pinger should carry the rule's fwmark, got %+v", got)
	}
	st := m.GetStatus()
	a, b := st[1], st[2]
	if a.WAN != "wanA" || b.WAN != "wanB" {
		t.Fatalf("order: %v", statusKeys(st))
	}
	if len(b.Unroutable) != 1 || b.Unroutable[0] != "ipv4" || len(b.ICMPv4) != 0 || len(b.HTTPSv4) != 0 || b.LossPct != 0 {
		t.Fatalf("unroutable WAN must have no samples: %+v", b)
	}
	if len(a.Unroutable) != 0 || len(a.ICMPv4) != 3 || a.LossPct != 0 || a.Egress["ipv4"] != "fwmark 0x6" {
		t.Fatalf("routable WAN: %+v", a)
	}
	if len(st[0].Unroutable) != 0 || st[0].Egress != nil {
		t.Fatalf("default route must be unaffected: %+v", st[0])
	}
}

// Routing changes are followed: the socket is reopened with the new egress
// and the stale history is dropped. Resolution errors fall back to plain
// interface binding.
func TestEgressChangeFollowed(t *testing.T) {
	egress := map[string]netutil.Egress{
		"wanA": {Iface: "wanA"},
		"wanB": {Iface: "wanB", Unroutable: "no ip rule"},
	}
	m, opened := policyMonitor(egress)
	defer m.Stop()
	m.probeAll()

	recheck := func() {
		for _, p := range m.snapshotPaths() {
			p.egAt = time.Time{}
		}
		m.probeAll()
	}

	// A fwmark rule for wanB's table appears.
	egress["wanB"] = netutil.Egress{Iface: "wanB", Mark: 0x9}
	recheck()
	if got := opened["wanB"]; len(got) != 1 || got[0].eg.Mark != 0x9 {
		t.Fatalf("wanB not probed with new mark: %+v", got)
	}
	st := m.GetStatus()
	if len(st[2].Unroutable) != 0 || len(st[2].ICMPv4) != 1 {
		t.Fatalf("wanB after rule added: %+v", st[2])
	}

	// wanA's rule changes from plain to source-bound: socket reopened,
	// history reset.
	if len(st[1].ICMPv4) != 2 {
		t.Fatalf("wanA history: %d", len(st[1].ICMPv4))
	}
	egress["wanA"] = netutil.Egress{Iface: "wanA", Src: net.ParseIP("192.0.2.9")}
	recheck()
	if got := opened["wanA"]; len(got) != 2 || !got[0].closed.Load() || !got[1].eg.Src.Equal(net.ParseIP("192.0.2.9")) {
		t.Fatalf("wanA not reopened with source: %+v", got)
	}
	if st := m.GetStatus(); len(st[1].ICMPv4) != 1 || st[1].Egress["ipv4"] != "source 192.0.2.9" {
		t.Fatalf("wanA history not reset: %+v", st[1])
	}

	// Unchanged egress: no reopen.
	recheck()
	if len(opened["wanA"]) != 2 {
		t.Fatalf("socket reopened without routing change")
	}

	// Routing state unreadable: plain interface binding.
	delete(egress, "wanB")
	recheck()
	if got := opened["wanB"]; len(got) != 2 || got[1].eg.Mark != 0 || got[1].eg.Iface != "wanB" {
		t.Fatalf("fallback: %+v", got)
	}
}

// A WAN whose egress is the default route is probed once and reported as
// the default; the marker follows default route changes.
func TestDefaultRouteMergedIntoWAN(t *testing.T) {
	opened := map[string][]*egressPinger{}
	m := newMonitor(testTargets[1:], func(v6 bool, eg netutil.Egress) (pinger, error) {
		if v6 {
			return nil, errors.New("no v6")
		}
		p := &egressPinger{eg: eg}
		opened[eg.Iface] = append(opened[eg.Iface], p)
		return p, nil
	})
	m.resolveEgress = plainEgress
	defIface := "wanA"
	m.sameAsDefault = func(eg netutil.Egress, _ net.IP) (bool, error) { return eg.Iface == defIface, nil }
	m.wanSource = func() []string { return []string{"wanA", "wanB"} }
	defer m.Stop()

	key := func() []string {
		var out []string
		for _, s := range m.GetStatus() {
			k := s.WAN
			if s.Default {
				k += "*"
			}
			out = append(out, k)
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}

	m.probeAll()
	m.probeAll()
	eq(key(), "wanA*", "wanB")
	if len(opened[""]) != 0 {
		t.Fatal("unbound default path probed although merged")
	}
	if st := m.GetStatus(); len(st[0].ICMPv4) != 2 {
		t.Fatalf("merged path history: %d", len(st[0].ICMPv4))
	}

	// Default route moves to wanB: marker moves, order follows.
	defIface = "wanB"
	m.defaultAt = time.Time{}
	m.probeAll()
	eq(key(), "wanB*", "wanA")

	// Default route via neither WAN: separate default-route entry again.
	defIface = "none"
	m.defaultAt = time.Time{}
	m.probeAll()
	eq(key(), "", "wanA", "wanB")
	if len(opened[""]) != 1 || len(m.GetStatus()[0].ICMPv4) != 1 {
		t.Fatal("default path not resumed with fresh history")
	}
}
