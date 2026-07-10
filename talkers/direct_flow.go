package talkers

import (
	"encoding/binary"
	"fmt"
)

const (
	protocolTCP = 6
	protocolUDP = 17
)

type directTransport struct {
	protocol         string
	srcPort, dstPort uint16
	hasPorts         bool
}

// parseDirectTransport parses transport metadata from the raw IP bytes delivered
// by the direct capture ring. It intentionally returns no port for malformed,
// truncated, or non-initial fragmented traffic.
func parseDirectTransport(packet []byte) directTransport {
	if len(packet) == 0 {
		return directTransport{protocol: "IP-0"}
	}
	switch packet[0] >> 4 {
	case 4:
		return parseDirectIPv4Transport(packet)
	case 6:
		return parseDirectIPv6Transport(packet)
	default:
		return directTransport{protocol: "IP-0"}
	}
}

func parseDirectIPv4Transport(packet []byte) directTransport {
	if len(packet) < 20 {
		return directTransport{protocol: "IP-0"}
	}
	protocol := packet[9]
	result := directTransport{protocol: protocolLabel(protocol)}
	headerLen := int(packet[0]&0x0f) * 4
	totalLen := int(binary.BigEndian.Uint16(packet[2:4]))
	if headerLen < 20 || headerLen > len(packet) || totalLen < headerLen {
		return result
	}
	end := minPacketEnd(totalLen, len(packet))
	fragment := binary.BigEndian.Uint16(packet[6:8])
	if fragment&0x1fff != 0 {
		return result
	}
	return parseDirectPorts(packet, headerLen, end, protocol, result)
}

func parseDirectIPv6Transport(packet []byte) directTransport {
	if len(packet) < 40 {
		return directTransport{protocol: "IP-0"}
	}
	next := packet[6]
	result := directTransport{protocol: protocolLabel(next)}
	payloadLen := int(binary.BigEndian.Uint16(packet[4:6]))
	// Jumbograms require validating a Hop-by-Hop Jumbo Payload option. Direct
	// capture does not need that rare case, so a zero payload length is kept in
	// the honest unknown-port bucket instead of parsing captured trailing bytes.
	if payloadLen == 0 {
		return result
	}
	end := len(packet)
	end = minPacketEnd(40+payloadLen, len(packet))
	offset := 40
	for headers := 0; headers < 16; headers++ {
		result.protocol = protocolLabel(next)
		switch next {
		case 0, 43, 60, 135, 139, 140:
			if offset+2 > end {
				return result
			}
			headerLen := (int(packet[offset+1]) + 1) * 8
			if headerLen < 8 || offset+headerLen > end {
				return result
			}
			next = packet[offset]
			offset += headerLen
		case 44:
			if offset+8 > end {
				return result
			}
			next = packet[offset]
			fragment := binary.BigEndian.Uint16(packet[offset+2 : offset+4])
			offset += 8
			if fragment&0xfff8 != 0 {
				return directTransport{protocol: protocolLabel(next)}
			}
		case 51:
			if offset+2 > end {
				return result
			}
			headerLen := (int(packet[offset+1]) + 2) * 4
			if headerLen < 8 || offset+headerLen > end {
				return result
			}
			next = packet[offset]
			offset += headerLen
		default:
			return parseDirectPorts(packet, offset, end, next, result)
		}
	}
	return result
}

func parseDirectPorts(packet []byte, offset, end int, protocol uint8, result directTransport) directTransport {
	required := 0
	switch protocol {
	case protocolTCP:
		required = 20
	case protocolUDP:
		required = 8
	default:
		return result
	}
	if offset < 0 || end < offset || offset+required > end || offset+required > len(packet) {
		return result
	}
	switch protocol {
	case protocolTCP:
		headerLen := int(packet[offset+12]>>4) * 4
		if headerLen < 20 || offset+headerLen > end || offset+headerLen > len(packet) {
			return result
		}
	case protocolUDP:
		udpLen := int(binary.BigEndian.Uint16(packet[offset+4 : offset+6]))
		if udpLen < 8 {
			return result
		}
	}
	result.srcPort = binary.BigEndian.Uint16(packet[offset : offset+2])
	result.dstPort = binary.BigEndian.Uint16(packet[offset+2 : offset+4])
	result.hasPorts = true
	return result
}

func minPacketEnd(declared, captured int) int {
	if declared < captured {
		return declared
	}
	return captured
}

func protocolLabel(protocol uint8) string {
	switch protocol {
	case 0:
		return "HOPOPT"
	case 1:
		return "ICMP"
	case 2:
		return "IGMP"
	case 4:
		return "IPIP"
	case protocolTCP:
		return "TCP"
	case protocolUDP:
		return "UDP"
	case 41:
		return "IPv6"
	case 43:
		return "ROUTING"
	case 44:
		return "FRAGMENT"
	case 47:
		return "GRE"
	case 50:
		return "ESP"
	case 51:
		return "AH"
	case 58:
		return "ICMPv6"
	case 59:
		return "NONE"
	case 60:
		return "DSTOPTS"
	case 132:
		return "SCTP"
	default:
		return fmt.Sprintf("IP-%d", protocol)
	}
}
