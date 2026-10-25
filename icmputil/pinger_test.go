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
