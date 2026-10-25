package collector

import (
	"bandwidth-monitor/packets"
	"fmt"
	"log"
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

// run opens the capture device and classifies packets until stopped.
func (s *spanOverlay) run() {
	handle, err := packets.FetchPcapSock(s.device, s.promisc)
	if err != nil {
		log.Printf("span: cannot open %s: %v", s.device, err)
		log.Println("span: requires root or CAP_NET_RAW")
		return
	}
	defer unix.Close(handle)

	if err := packets.ApplyBPFFilter(handle, packets.BPFFilterForDevice(s.device)); err != nil {
		fmt.Fprintf(os.Stderr, "span: BPF filter error: %v\n", err)
	}

	epfd, err := packets.CreateEpoller(handle)
	if err != nil {
		log.Printf("span: failed to setup epoller on %s: %v", s.device, err)
		return
	}
	defer unix.Close(epfd)

	log.Printf("span: capturing on %s (promiscuous=%v, %d local nets)",
		s.device, s.promisc, len(s.localNets))

	events := make([]unix.EpollEvent, 128)
	data := make([]byte, packets.SnapLen)

	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		n, err := unix.EpollWait(epfd, events, 100)
		if err != nil {
			continue
		}
		for i := 0; i < n; i++ {
			if int(events[i].Fd) == handle {
				numRead, _, err := unix.Recvfrom(handle, data, 0)
				if err != nil {
					log.Printf("span: read error on %s: %v", s.device, err)
					return
				}
				s.processPacket(data[:numRead])
			}
		}
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
	ipPacket := packets.ParseIPPacket(pkt, s.isL3)
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
