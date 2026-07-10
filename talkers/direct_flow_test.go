package talkers

import (
	"encoding/binary"
	"testing"
)

func TestParseDirectIPv4TransportOptionsFragmentsAndBounds(t *testing.T) {
	tcp := transportHeader(protocolTCP, 49152, 65535)
	packet := ipv4Packet(protocolTCP, 24, 0, tcp)
	got := parseDirectTransport(packet)
	assertTransport(t, got, "TCP", 49152, 65535, true)

	initial := ipv4Packet(protocolUDP, 20, 0x2000, transportHeader(protocolUDP, 53000, 53))
	assertTransport(t, parseDirectTransport(initial), "UDP", 53000, 53, true)

	later := ipv4Packet(protocolTCP, 20, 1, []byte{0xff, 0xff, 0xff, 0xff})
	assertTransport(t, parseDirectTransport(later), "TCP", 0, 0, false)

	for _, malformed := range [][]byte{
		nil,
		make([]byte, 19),
		ipv4Packet(protocolTCP, 16, 0, tcp),
		ipv4Packet(protocolTCP, 24, 0, []byte{1, 2}),
	} {
		_ = parseDirectTransport(malformed)
	}
	truncated := ipv4Packet(protocolUDP, 20, 0, []byte{0, 1, 0, 2})
	assertTransport(t, parseDirectTransport(truncated), "UDP", 0, 0, false)
	badTCP := transportHeader(protocolTCP, 1, 2)
	badTCP[12] = 4 << 4
	assertTransport(t, parseDirectTransport(ipv4Packet(protocolTCP, 20, 0, badTCP)), "TCP", 0, 0, false)
	badUDP := transportHeader(protocolUDP, 1, 2)
	binary.BigEndian.PutUint16(badUDP[4:6], 7)
	assertTransport(t, parseDirectTransport(ipv4Packet(protocolUDP, 20, 0, badUDP)), "UDP", 0, 0, false)
}

func TestParseDirectIPv6ExtensionChainsFragmentsAndBounds(t *testing.T) {
	tcp := transportHeader(protocolTCP, 40000, 443)
	hop := extensionHeader(60, 0)
	destination := extensionHeader(protocolTCP, 0)
	packet := ipv6Packet(0, append(append(hop, destination...), tcp...))
	assertTransport(t, parseDirectTransport(packet), "TCP", 40000, 443, true)

	initialFragment := fragmentHeader(protocolUDP, 0)
	packet = ipv6Packet(44, append(initialFragment, transportHeader(protocolUDP, 5353, 53)...))
	assertTransport(t, parseDirectTransport(packet), "UDP", 5353, 53, true)

	laterFragment := fragmentHeader(protocolTCP, 8)
	packet = ipv6Packet(44, append(laterFragment, []byte{0xff, 0xff, 0xff, 0xff}...))
	assertTransport(t, parseDirectTransport(packet), "TCP", 0, 0, false)

	malformedExtension := ipv6Packet(0, []byte{protocolTCP, 10, 0, 0, 0, 0, 0, 0})
	assertTransport(t, parseDirectTransport(malformedExtension), "HOPOPT", 0, 0, false)
	assertTransport(t, parseDirectTransport(make([]byte, 39)), "IP-0", 0, 0, false)
	zeroPayload := ipv6Packet(protocolTCP, transportHeader(protocolTCP, 1, 443))
	binary.BigEndian.PutUint16(zeroPayload[4:6], 0)
	assertTransport(t, parseDirectTransport(zeroPayload), "TCP", 0, 0, false)
}

func TestParseDirectTransportLabelsProtocolsWithoutPorts(t *testing.T) {
	for protocol, want := range map[uint8]string{
		1: "ICMP", 47: "GRE", 50: "ESP", 58: "ICMPv6", 253: "IP-253",
	} {
		packet := ipv4Packet(protocol, 20, 0, make([]byte, 8))
		if protocol == 58 {
			packet = ipv6Packet(protocol, make([]byte, 8))
		}
		assertTransport(t, parseDirectTransport(packet), want, 0, 0, false)
	}
}

func assertTransport(t *testing.T, got directTransport, protocol string, src, dst uint16, hasPorts bool) {
	t.Helper()
	if got.protocol != protocol || got.srcPort != src || got.dstPort != dst || got.hasPorts != hasPorts {
		t.Fatalf("got %+v, want protocol=%s src=%d dst=%d hasPorts=%v", got, protocol, src, dst, hasPorts)
	}
}

func ipv4Packet(protocol uint8, headerLen int, fragment uint16, payload []byte) []byte {
	if headerLen < 20 {
		packet := make([]byte, 20+len(payload))
		packet[0] = 0x44
		packet[9] = protocol
		binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
		copy(packet[20:], payload)
		return packet
	}
	packet := make([]byte, headerLen+len(payload))
	packet[0] = 0x40 | byte(headerLen/4)
	packet[9] = protocol
	binary.BigEndian.PutUint16(packet[2:4], uint16(len(packet)))
	binary.BigEndian.PutUint16(packet[6:8], fragment)
	copy(packet[headerLen:], payload)
	return packet
}

func ipv6Packet(next uint8, payload []byte) []byte {
	packet := make([]byte, 40+len(payload))
	packet[0] = 0x60
	packet[6] = next
	binary.BigEndian.PutUint16(packet[4:6], uint16(len(payload)))
	copy(packet[40:], payload)
	return packet
}

func transportHeader(protocol uint8, src, dst uint16) []byte {
	size := 20
	if protocol == protocolUDP {
		size = 8
	}
	header := make([]byte, size)
	binary.BigEndian.PutUint16(header[0:2], src)
	binary.BigEndian.PutUint16(header[2:4], dst)
	if protocol == protocolTCP {
		header[12] = 5 << 4
	} else {
		binary.BigEndian.PutUint16(header[4:6], uint16(size))
	}
	return header
}

func extensionHeader(next uint8, length uint8) []byte {
	header := make([]byte, (int(length)+1)*8)
	header[0], header[1] = next, length
	return header
}

func fragmentHeader(next uint8, offset uint16) []byte {
	header := make([]byte, 8)
	header[0] = next
	binary.BigEndian.PutUint16(header[2:4], offset)
	return header
}
