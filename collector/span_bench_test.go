package collector

import (
	"net"
	"testing"
)

func BenchmarkSpanProcessPacket(b *testing.B) {
	_, lan, _ := net.ParseCIDR("192.0.2.0/24")
	s := newSpanOverlay("eth0", true, []*net.IPNet{lan})
	frame := make([]byte, 54)
	frame[12], frame[13] = 0x08, 0x00
	ip := frame[14:]
	ip[0], ip[3], ip[9] = 0x45, 40, 6
	copy(ip[12:16], []byte{192, 0, 2, 10})
	copy(ip[16:20], []byte{198, 51, 100, 7})
	b.ReportAllocs()
	for b.Loop() {
		s.processPacket(frame)
	}
	if s.txPackets == 0 {
		b.Fatal("packet not classified as upload")
	}
}
