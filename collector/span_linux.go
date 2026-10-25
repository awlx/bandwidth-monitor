//go:build linux

package collector

import (
	"bandwidth-monitor/packets"
	"fmt"
	"log"
	"os"

	"golang.org/x/sys/unix"
)

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
