//go:build darwin || freebsd

package collector

import (
	"log"

	"bandwidth-monitor/packets"
)

// run opens a BPF capture on the SPAN device and classifies packets until
// stopped. The BPF ring already normalizes frames to raw IP packets.
func (s *spanOverlay) run() {
	ring, err := packets.NewRing(s.device, s.promisc)
	if err != nil {
		log.Printf("span: cannot open %s: %v", s.device, err)
		return
	}
	defer ring.Close()

	log.Printf("span: capturing on %s via BPF (promiscuous=%v, %d local nets)",
		s.device, s.promisc, len(s.localNets))

	handler := func(pkt []byte, _ uint32) { s.processL3Packet(pkt) }
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		if _, err := ring.ReadBlock(handler, 100); err != nil {
			log.Printf("span: read error on %s: %v", s.device, err)
			return
		}
	}
}
