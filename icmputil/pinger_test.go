package icmputil

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type pkt struct {
	b    []byte
	from net.Addr
}

// fakeConn simulates a raw ICMP socket. onWrite is called for every echo
// request and may inject replies via deliver.
type fakeConn struct {
	v6      bool
	rx      chan pkt
	closed  chan struct{}
	once    sync.Once
	onWrite func(c *fakeConn, id, seq uint16, dst net.IP)
}

func newFakeConn(v6 bool, onWrite func(c *fakeConn, id, seq uint16, dst net.IP)) *fakeConn {
	return &fakeConn{v6: v6, rx: make(chan pkt, 64), closed: make(chan struct{}), onWrite: onWrite}
}

func (c *fakeConn) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case p := <-c.rx:
		return copy(b, p.b), p.from, nil
	case <-c.closed:
		return 0, nil, errors.New("closed")
	}
}

func (c *fakeConn) WriteTo(b []byte, dst net.Addr) (int, error) {
	proto := 1
	if c.v6 {
		proto = 58
	}
	m, err := icmp.ParseMessage(proto, b)
	if err != nil {
		return 0, err
	}
	e := m.Body.(*icmp.Echo)
	if c.onWrite != nil {
		c.onWrite(c, uint16(e.ID), uint16(e.Seq), dst.(*net.IPAddr).IP)
	}
	return len(b), nil
}

func (c *fakeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

// deliver injects an echo reply after delay.
func (c *fakeConn) deliver(delay time.Duration, from net.IP, id, seq uint16) {
	var typ icmp.Type = ipv4.ICMPTypeEchoReply
	if c.v6 {
		typ = ipv6.ICMPTypeEchoReply
	}
	b, _ := (&icmp.Message{Type: typ, Body: &icmp.Echo{ID: int(id), Seq: int(seq), Data: []byte("bwmon")}}).Marshal(nil)
	go func() {
		time.Sleep(delay)
		select {
		case c.rx <- pkt{b: b, from: &net.IPAddr{IP: from}}:
		case <-c.closed:
		}
	}()
}

var (
	dstA = net.ParseIP("192.0.2.1").To4()
	dstB = net.ParseIP("192.0.2.2").To4()
)

func TestPingerReply(t *testing.T) {
	for _, v6 := range []bool{false, true} {
		dst := dstA
		if v6 {
			dst = net.ParseIP("2001:db8::1")
		}
		c := newFakeConn(v6, func(c *fakeConn, id, seq uint16, d net.IP) {
			c.deliver(5*time.Millisecond, d, id, seq)
		})
		p := NewPingerConn(c, v6, 0x1234)
		if rtt := p.Ping(dst, time.Second); rtt < 0 {
			t.Fatalf("v6=%v: expected reply, got loss", v6)
		}
		p.Close()
	}
}

// Replies that only match the identifier must not be accepted: a stale
// reply (old seq), a reply from another host, or another process's ID.
func TestPingerIgnoresMismatchedReplies(t *testing.T) {
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		c.deliver(time.Millisecond, d, id, seq-1)  // stale seq
		c.deliver(time.Millisecond, dstB, id, seq) // wrong source
		c.deliver(time.Millisecond, d, id+1, seq)  // foreign id
	})
	p := NewPingerConn(c, false, 0x4242)
	defer p.Close()
	if rtt := p.Ping(dstA, 100*time.Millisecond); rtt >= 0 {
		t.Fatalf("mismatched reply accepted (rtt=%v)", rtt)
	}
}

// A reply arriving after its probe timed out must not be credited to the
// next probe to the same destination.
func TestPingerLateReplyNotReused(t *testing.T) {
	var mu sync.Mutex
	n := 0
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		mu.Lock()
		defer mu.Unlock()
		n++
		if n == 1 {
			c.deliver(60*time.Millisecond, d, id, seq) // arrives late
		}
	})
	p := NewPingerConn(c, false, 0x1111)
	defer p.Close()
	if rtt := p.Ping(dstA, 30*time.Millisecond); rtt >= 0 {
		t.Fatalf("first probe should time out, got %v", rtt)
	}
	if rtt := p.Ping(dstA, 100*time.Millisecond); rtt >= 0 {
		t.Fatalf("late reply of first probe credited to second (rtt=%v)", rtt)
	}
}

// Concurrent probes to different targets each get their own reply even
// when replies arrive out of order.
func TestPingerConcurrentOutOfOrder(t *testing.T) {
	delays := map[string]time.Duration{
		"192.0.2.1": 80 * time.Millisecond,
		"192.0.2.2": 40 * time.Millisecond,
		"192.0.2.3": 5 * time.Millisecond,
	}
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		c.deliver(delays[d.String()], d, id, seq)
	})
	p := NewPingerConn(c, false, 0x2222)
	defer p.Close()

	var wg sync.WaitGroup
	res := make(map[string]float64)
	var mu sync.Mutex
	for ip := range delays {
		wg.Add(1)
		go func(ip string) {
			defer wg.Done()
			rtt := p.Ping(net.ParseIP(ip).To4(), time.Second)
			mu.Lock()
			res[ip] = rtt
			mu.Unlock()
		}(ip)
	}
	wg.Wait()
	for ip, d := range delays {
		rtt := res[ip]
		want := float64(d.Milliseconds())
		if rtt < want || rtt > want+60 {
			t.Errorf("%s: rtt=%.1fms, want ~%.0fms", ip, rtt, want)
		}
	}
}

func TestPingerSeqIncrements(t *testing.T) {
	var mu sync.Mutex
	var seqs []uint16
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		mu.Lock()
		seqs = append(seqs, seq)
		mu.Unlock()
		c.deliver(0, d, id, seq)
	})
	p := NewPingerConn(c, false, 0x3333)
	defer p.Close()
	for i := 0; i < 3; i++ {
		p.Ping(dstA, time.Second)
	}
	if len(seqs) != 3 || seqs[0] == seqs[1] || seqs[1] == seqs[2] {
		t.Fatalf("seq not incrementing: %v", seqs)
	}
}

func TestPingerClose(t *testing.T) {
	p := NewPingerConn(newFakeConn(false, nil), false, 1)
	done := make(chan float64)
	go func() { done <- p.Ping(dstA, 5*time.Second) }()
	time.Sleep(20 * time.Millisecond)
	p.Close()
	select {
	case rtt := <-done:
		if rtt >= 0 {
			t.Fatalf("expected loss after close, got %v", rtt)
		}
	case <-time.After(time.Second):
		t.Fatal("Ping did not return after Close")
	}
	if rtt := p.Ping(dstA, time.Second); rtt >= 0 {
		t.Fatal("Ping after Close should fail")
	}
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func withClock(p *Pinger, c *fakeClock) {
	p.mu.Lock()
	p.now = c.now
	p.idSince = c.now()
	p.mu.Unlock()
}

// The identifier stays constant inside one rotate interval and changes
// after it.
func TestPingerIDRotation(t *testing.T) {
	var mu sync.Mutex
	var ids []uint16
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		mu.Lock()
		ids = append(ids, id)
		mu.Unlock()
		c.deliver(0, d, id, seq)
	})
	p := NewPingerConn(c, false, 0x5555)
	defer p.Close()
	clk := &fakeClock{t: time.Unix(1000, 0)}
	withClock(p, clk)

	for i := 0; i < 3; i++ {
		if rtt := p.Ping(dstA, time.Second); rtt < 0 {
			t.Fatalf("probe %d lost", i)
		}
		clk.advance(DefaultIDRotation / 4)
	}
	clk.advance(DefaultIDRotation)
	if rtt := p.Ping(dstA, time.Second); rtt < 0 {
		t.Fatal("probe after rotation lost")
	}
	if p.ID() == 0x5555 {
		t.Fatal("ID() did not change after rotation")
	}
	clk.advance(DefaultIDRotation)
	p.Ping(dstA, time.Second)

	if len(ids) != 5 {
		t.Fatalf("got %d probes, want 5", len(ids))
	}
	for i := 0; i < 3; i++ {
		if ids[i] != 0x5555 {
			t.Fatalf("probe %d used id %#x before rotation, want 0x5555", i, ids[i])
		}
	}
	if ids[3] == ids[2] || ids[4] == ids[3] {
		t.Fatalf("id not rotated: %#x", ids)
	}
}

func TestPingerIDRotationDisabled(t *testing.T) {
	var mu sync.Mutex
	var ids []uint16
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		mu.Lock()
		ids = append(ids, id)
		mu.Unlock()
		c.deliver(0, d, id, seq)
	})
	p := NewPingerConn(c, false, 0x6666)
	defer p.Close()
	p.SetIDRotation(0)
	clk := &fakeClock{t: time.Unix(1000, 0)}
	withClock(p, clk)
	for i := 0; i < 3; i++ {
		p.Ping(dstA, time.Second)
		clk.advance(time.Hour)
	}
	for _, id := range ids {
		if id != 0x6666 {
			t.Fatalf("id changed with rotation disabled: %#x", ids)
		}
	}
}

// A probe sent under the old identifier still gets its reply when the
// reply arrives after the identifier changed.
func TestPingerInFlightAcrossRotation(t *testing.T) {
	type sent struct {
		id, seq uint16
		dst     net.IP
	}
	sentCh := make(chan sent, 4)
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		sentCh <- sent{id, seq, d}
	})
	p := NewPingerConn(c, false, 0x7777)
	defer p.Close()
	clk := &fakeClock{t: time.Unix(1000, 0)}
	withClock(p, clk)

	oldRes := make(chan float64, 1)
	go func() { oldRes <- p.Ping(dstA, time.Second) }()
	old := <-sentCh

	clk.advance(DefaultIDRotation)
	newRes := make(chan float64, 1)
	go func() { newRes <- p.Ping(dstB, time.Second) }()
	cur := <-sentCh
	if cur.id == old.id {
		t.Fatalf("id did not rotate (%#x)", cur.id)
	}

	// Reply to the new probe first, then the late reply to the old probe.
	c.deliver(0, cur.dst, cur.id, cur.seq)
	c.deliver(10*time.Millisecond, old.dst, old.id, old.seq)
	if rtt := <-newRes; rtt < 0 {
		t.Fatal("probe under new id lost")
	}
	if rtt := <-oldRes; rtt < 0 {
		t.Fatal("in-flight probe under old id lost across rotation")
	}
}

// After a rotation, a reply that carries the old identifier must not be
// credited to a new probe with the same sequence number and destination.
func TestPingerStaleIDNotCrossMatched(t *testing.T) {
	sentCh := make(chan [2]uint16, 4)
	c := newFakeConn(false, func(c *fakeConn, id, seq uint16, d net.IP) {
		sentCh <- [2]uint16{id, seq}
	})
	p := NewPingerConn(c, false, 0x8888)
	defer p.Close()
	clk := &fakeClock{t: time.Unix(1000, 0)}
	withClock(p, clk)
	clk.advance(DefaultIDRotation)

	res := make(chan float64, 1)
	go func() { res <- p.Ping(dstA, 100*time.Millisecond) }()
	s := <-sentCh
	if s[0] == 0x8888 {
		t.Fatal("id did not rotate")
	}
	// Same seq and source, but the previous identifier.
	c.deliver(time.Millisecond, dstA, 0x8888, s[1])
	if rtt := <-res; rtt >= 0 {
		t.Fatalf("reply with stale id accepted (rtt=%v)", rtt)
	}
}
