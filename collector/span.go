package collector

import (
	"bandwidth-monitor/packets"
	"net"
	"sync"
	"time"
)

const (
	spanSnapshotLen = 128 // only need IP headers for direction + length
	spanCapTimeout  = 100 * time.Millisecond
)

// spanOverlay captures packets on a SPAN/mirror port and classifies traffic
// direction using LOCAL_NETS. It feeds direction-aware RX/TX counters back
// to the main Collector, which uses them to override the kernel interface
// counters for the SPAN device.
type spanOverlay struct {
	device    string
	isL3      bool // cached: the interface lookup is a netlink dump
	promisc   bool
	localNets []*net.IPNet

	accMu     sync.Mutex
	rxBytes   uint64
	txBytes   uint64
	rxPackets uint64
	txPackets uint64

	stopCh chan struct{}
}

func newSpanOverlay(device string, promisc bool, localNets []*net.IPNet) *spanOverlay {
	return &spanOverlay{
		device:    device,
		isL3:      packets.IsL3Device(device),
		promisc:   promisc,
		localNets: localNets,
		stopCh:    make(chan struct{}),
	}
}

func (s *spanOverlay) stop() {
	close(s.stopCh)
}

// snapshot returns the current cumulative counters.
func (s *spanOverlay) snapshot() (rxBytes, txBytes, rxPackets, txPackets uint64) {
	s.accMu.Lock()
	rxBytes = s.rxBytes
	txBytes = s.txBytes
	rxPackets = s.rxPackets
	txPackets = s.txPackets
	s.accMu.Unlock()
	return
}

func (s *spanOverlay) processPacket(pkt []byte) {
	s.account(packets.ParseIPPacket(pkt, s.isL3))
}

func (s *spanOverlay) processL3Packet(pkt []byte) {
	s.account(packets.ParseIPPacket(pkt, true))
}

func (s *spanOverlay) account(ipPacket packets.Packet) {
	if ipPacket.Version == 0 {
		return // unparseable packet
	}

	srcLocal := s.isLocal(ipPacket.SrcIP)
	dstLocal := s.isLocal(ipPacket.DstIP)

	s.accMu.Lock()
	switch {
	case srcLocal && !dstLocal:
		// local → remote = upload (TX)
		s.txBytes += ipPacket.Len
		s.txPackets++
	case !srcLocal && dstLocal:
		// remote → local = download (RX)
		s.rxBytes += ipPacket.Len
		s.rxPackets++
	case srcLocal && dstLocal:
		// intra-LAN — count as both
		s.rxBytes += ipPacket.Len
		s.rxPackets++
		s.txBytes += ipPacket.Len
		s.txPackets++
	}
	// both-remote packets (shouldn't appear on a properly-filtered SPAN) are ignored
	s.accMu.Unlock()
}

func (s *spanOverlay) isLocal(ip net.IP) bool {
	for _, n := range s.localNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
