package icmputil

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"sync"
	"time"

	"bandwidth-monitor/netutil"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// PacketConn is the subset of *icmp.PacketConn used by Pinger.
type PacketConn interface {
	ReadFrom(b []byte) (int, net.Addr, error)
	WriteTo(b []byte, dst net.Addr) (int, error)
	Close() error
}

// ErrClosed is returned by Ping after Close.
var ErrClosed = errors.New("icmputil: pinger closed")

// Pinger sends ICMP echo requests over one long-lived raw socket and
// dispatches replies from a single reader goroutine. Raw ICMP sockets see
// every echo reply on the host, so replies are matched on identifier,
// sequence number and source address; stale replies from earlier probes
// and replies meant for other processes are ignored.
type Pinger struct {
	conn  PacketConn
	proto int // 1 = ICMPv4, 58 = ICMPv6
	id    uint16

	mu      sync.Mutex
	seq     uint16
	pending map[uint16]*pendingProbe
	closed  bool
	done    chan struct{}
}

type pendingProbe struct {
	dst   net.IP
	start time.Time
	ch    chan float64
}

// NewPinger opens a raw ICMP socket ("ip4:icmp" or "ip6:ipv6-icmp").
// It requires root or CAP_NET_RAW.
func NewPinger(v6 bool) (*Pinger, error) {
	return NewPingerInterface(v6, "")
}

// NewPingerInterface is like NewPinger but binds the socket to iface
// (Linux SO_BINDTODEVICE). An empty iface uses the default route.
func NewPingerInterface(v6 bool, iface string) (*Pinger, error) {
	return NewPingerEgress(v6, netutil.Egress{Iface: iface})
}

// NewPingerEgress is like NewPinger but sends via e: interface binding,
// firewall mark and source address (see netutil.ResolveEgress).
func NewPingerEgress(v6 bool, e netutil.Egress) (*Pinger, error) {
	network, addr := "ip4:icmp", "0.0.0.0"
	if v6 {
		network, addr = "ip6:ipv6-icmp", "::"
	}
	if e.Src != nil {
		addr = e.Src.String()
	}
	var c PacketConn
	var err error
	if e.Iface == "" && e.Mark == 0 {
		c, err = icmp.ListenPacket(network, addr)
	} else {
		c, err = netutil.ListenConfigForEgress(e).ListenPacket(context.Background(), network, addr)
	}
	if err != nil {
		return nil, err
	}
	return NewPingerConn(c, v6, randomID()), nil
}

// NewPingerConn wraps an existing connection. It is mainly for tests.
func NewPingerConn(conn PacketConn, v6 bool, id uint16) *Pinger {
	p := &Pinger{
		conn:    conn,
		proto:   1,
		id:      id,
		seq:     randomID(),
		pending: make(map[uint16]*pendingProbe),
		done:    make(chan struct{}),
	}
	if v6 {
		p.proto = 58
	}
	go p.readLoop()
	return p
}

// ID returns the ICMP echo identifier used by this pinger.
func (p *Pinger) ID() uint16 { return p.id }

// Ping sends one echo request to dst and waits up to timeout for the
// matching reply. It returns the RTT in milliseconds, or -1 on loss/error.
// It is safe for concurrent use.
func (p *Pinger) Ping(dst net.IP, timeout time.Duration) float64 {
	pr := &pendingProbe{dst: dst, ch: make(chan float64, 1)}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return -1
	}
	// Skip sequence numbers still in flight (only possible with >65k
	// concurrent probes, but keeps matching unambiguous).
	for i := 0; i < 1<<16; i++ {
		p.seq++
		if _, busy := p.pending[p.seq]; !busy {
			break
		}
	}
	seq := p.seq
	p.pending[seq] = pr
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		if p.pending[seq] == pr {
			delete(p.pending, seq)
		}
		p.mu.Unlock()
	}()

	wb, err := p.marshalEcho(seq)
	if err != nil {
		return -1
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	p.mu.Lock()
	pr.start = time.Now()
	p.mu.Unlock()
	if _, err := p.conn.WriteTo(wb, &net.IPAddr{IP: dst}); err != nil {
		return -1
	}

	select {
	case rtt := <-pr.ch:
		return rtt
	case <-timer.C:
		return -1
	case <-p.done:
		return -1
	}
}

// Close closes the socket and stops the reader goroutine.
func (p *Pinger) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	close(p.done)
	p.mu.Unlock()
	return p.conn.Close()
}

func (p *Pinger) marshalEcho(seq uint16) ([]byte, error) {
	var typ icmp.Type = ipv4.ICMPTypeEcho
	if p.proto == 58 {
		typ = ipv6.ICMPTypeEchoRequest
	}
	msg := icmp.Message{
		Type: typ,
		Body: &icmp.Echo{ID: int(p.id), Seq: int(seq), Data: []byte("bwmon")},
	}
	return msg.Marshal(nil)
}

func (p *Pinger) readLoop() {
	rb := make([]byte, 1500)
	for {
		n, from, err := p.conn.ReadFrom(rb)
		if err != nil {
			select {
			case <-p.done:
				return
			default:
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			// Unexpected read error: fail all in-flight probes by closing.
			p.Close()
			return
		}
		now := time.Now()
		seq, ok := p.parseReply(rb[:n])
		if !ok {
			continue
		}
		src := addrIP(from)
		p.mu.Lock()
		pr := p.pending[seq]
		if pr != nil && src != nil && pr.dst.Equal(src) {
			delete(p.pending, seq)
			rtt := float64(now.Sub(pr.start).Microseconds()) / 1000.0
			p.mu.Unlock()
			pr.ch <- rtt
			continue
		}
		p.mu.Unlock()
	}
}

// parseReply returns the sequence number if b is an echo reply carrying
// this pinger's identifier.
func (p *Pinger) parseReply(b []byte) (uint16, bool) {
	rm, err := icmp.ParseMessage(p.proto, b)
	if err != nil {
		return 0, false
	}
	if rm.Type != ipv4.ICMPTypeEchoReply && rm.Type != ipv6.ICMPTypeEchoReply {
		return 0, false
	}
	echo, ok := rm.Body.(*icmp.Echo)
	if !ok || uint16(echo.ID) != p.id {
		return 0, false
	}
	return uint16(echo.Seq), true
}

func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPAddr:
		return v.IP
	case *net.UDPAddr:
		return v.IP
	}
	return nil
}

func randomID() uint16 {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return uint16(time.Now().UnixNano())
	}
	return binary.BigEndian.Uint16(b[:])
}
