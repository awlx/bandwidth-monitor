package collector

import (
	"bandwidth-monitor/packets"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	spanSnapshotLen = 128 // only need IP headers for direction + length
	spanCapTimeout  = 100 * time.Millisecond
)

// spanOverlay captures packets on a SPAN/mirror port and classifies traffic
// direction using LOCAL_NETS. It feeds direction-aware RX/TX counters back
// to the main Collector, which uses them to override the /proc/net/dev
// numbers for the SPAN device.
type spanOverlay struct {
	device    string
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
		promisc:   promisc,
		localNets: localNets,
		stopCh:    make(chan struct{}),
	}
}

// run opens the capture device and classifies packets until stopped.
func (s *spanOverlay) run() {
	handle, err := packets.FetchPcapSock(s.device)
	if err != nil {
		fmt.Fprintf(os.Stderr, "span: cannot open %s: %v\n", s.device, err)
		fmt.Fprintln(os.Stderr, "span: pcap requires root or CAP_NET_RAW")
		return
	}
	defer unix.Close(handle)

	if err := packets.ApplyBPFFilter(handle, packets.AnyIpFilter); err != nil {
		fmt.Fprintf(os.Stderr, "span: BPF filter error: %v\n", err)
	}

	fmt.Fprintf(os.Stderr, "span: capturing on %s (promiscuous=%v, %d local nets)\n",
		s.device, s.promisc, len(s.localNets))

	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		data := make([]byte, packets.SnapLen)
		numRead, _, err := unix.Recvfrom(handle, data, 0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "span: read error on %s: %v\n", s.device, err)
			return
		}
		s.processPacket(data[:numRead])
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
	ipPacket := packets.ParseIPPacket(pkt)

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
