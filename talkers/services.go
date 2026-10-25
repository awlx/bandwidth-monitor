package talkers

import "encoding/binary"

// serviceID indexes the fixed service table. The per-packet path stores bytes
// in a fixed-size array indexed by serviceID, so it never allocates.
type serviceID uint8

const (
	svcOther serviceID = iota
	svcHTTPS
	svcQUIC
	svcHTTP
	svcDNS
	svcDoT
	svcDoQ
	svcSSH
	svcNTP
	svcWireGuard
	svcOpenVPN
	svcIPsec
	svcMail
	svcOtherTCP
	svcOtherUDP
	svcICMP
	numServices
)

// serviceNames are the label values exposed in the API and in metrics. The
// set is fixed, so metric cardinality stays bounded.
var serviceNames = [numServices]string{
	svcOther:     "Other",
	svcHTTPS:     "HTTPS",
	svcQUIC:      "QUIC",
	svcHTTP:      "HTTP",
	svcDNS:       "DNS",
	svcDoT:       "DoT",
	svcDoQ:       "DoQ",
	svcSSH:       "SSH",
	svcNTP:       "NTP",
	svcWireGuard: "WireGuard",
	svcOpenVPN:   "OpenVPN",
	svcIPsec:     "IPsec",
	svcMail:      "Mail",
	svcOtherTCP:  "Other TCP",
	svcOtherUDP:  "Other UDP",
	svcICMP:      "ICMP",
}

const (
	ipProtoICMP   = 1
	ipProtoESP    = 50
	ipProtoAH     = 51
	ipProtoICMPv6 = 58
)

// tcpService maps a well-known TCP port to a service, or svcOther.
func tcpService(port uint16) serviceID {
	switch port {
	case 443, 8443:
		return svcHTTPS
	case 80, 8080:
		return svcHTTP
	case 53:
		return svcDNS
	case 853:
		return svcDoT
	case 22:
		return svcSSH
	case 1194:
		return svcOpenVPN
	case 25, 465, 587, 110, 995, 143, 993:
		return svcMail
	}
	return svcOther
}

// udpService maps a well-known UDP port to a service, or svcOther.
func udpService(port uint16) serviceID {
	switch port {
	case 443:
		return svcQUIC
	case 53:
		return svcDNS
	case 853:
		return svcDoQ
	case 123:
		return svcNTP
	case 51820:
		return svcWireGuard
	case 1194:
		return svcOpenVPN
	case 500, 4500:
		return svcIPsec
	}
	return svcOther
}

// classifyService picks the service for one packet. Both ports are checked
// against the well-known table; when both match, the lower port wins because
// it is most likely the server side.
func classifyService(ipProto uint8, srcPort, dstPort uint16, hasPorts bool) serviceID {
	var tcp bool
	var fallback serviceID
	switch ipProto {
	case protocolTCP:
		tcp, fallback = true, svcOtherTCP
	case protocolUDP:
		fallback = svcOtherUDP
	case ipProtoICMP, ipProtoICMPv6:
		return svcICMP
	case ipProtoESP, ipProtoAH:
		return svcIPsec
	default:
		return svcOther
	}
	if !hasPorts {
		return fallback
	}
	lo, hi := srcPort, dstPort
	if hi < lo {
		lo, hi = hi, lo
	}
	if s := portService(tcp, lo); s != svcOther {
		return s
	}
	if s := portService(tcp, hi); s != svcOther {
		return s
	}
	return fallback
}

func portService(tcp bool, port uint16) serviceID {
	if tcp {
		return tcpService(port)
	}
	return udpService(port)
}

// quickL4Ports reads the TCP/UDP ports from a raw IP packet. It returns the
// L4 protocol number and ok=false when the ports are not available (short
// packet, non-initial fragment, or IPv6 extension headers). It is a cheaper
// variant of parseDirectTransport for the non-direct capture path.
func quickL4Ports(pkt []byte) (proto uint8, srcPort, dstPort uint16, ok bool) {
	if len(pkt) == 0 {
		return 0, 0, 0, false
	}
	var off int
	switch pkt[0] >> 4 {
	case 4:
		if len(pkt) < 20 {
			return 0, 0, 0, false
		}
		proto = pkt[9]
		if binary.BigEndian.Uint16(pkt[6:8])&0x1fff != 0 {
			return proto, 0, 0, false
		}
		off = int(pkt[0]&0x0f) * 4
		if off < 20 {
			return proto, 0, 0, false
		}
	case 6:
		if len(pkt) < 40 {
			return 0, 0, 0, false
		}
		proto = pkt[6]
		off = 40
	default:
		return 0, 0, 0, false
	}
	if proto != protocolTCP && proto != protocolUDP {
		return proto, 0, 0, false
	}
	if off+4 > len(pkt) {
		return proto, 0, 0, false
	}
	return proto, binary.BigEndian.Uint16(pkt[off : off+2]), binary.BigEndian.Uint16(pkt[off+2 : off+4]), true
}
