package latency

import (
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
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

func newTestMonitor(p4, p6 pinger, httpDelay time.Duration) *Monitor {
	targets := []resolvedTarget{
		{name: "a.example", ipv4: net.ParseIP("192.0.2.1").To4(), ipv6: net.ParseIP("2001:db8::1")},
		{name: "b.example", ipv4: net.ParseIP("192.0.2.2").To4()},
	}
	m := &Monitor{targets: targets, state: map[string]*targetState{}}
	m.Runner.Init()
	m.newPinger = func(v6 bool) (pinger, error) {
		if v6 {
			if p6 == nil {
				return nil, errors.New("no v6")
			}
			return p6, nil
		}
		return p4, nil
	}
	c := &http.Client{Transport: slowTransport{httpDelay}}
	for _, t := range targets {
		m.state[t.name] = &targetState{httpC4: c, httpC6: c}
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
	m.newPinger = func(v6 bool) (pinger, error) {
		if !v6 {
			opens++
		}
		return inner(v6)
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
