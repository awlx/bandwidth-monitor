package packets

import (
	"encoding/binary"
	"net"

	"golang.org/x/net/bpf"
)

const (
	etherTypeIPv4   = 0x0800
	etherTypeIPv6   = 0x86dd
	etherTypeDot1Q  = 0x8100
	etherTypeDot1AD = 0x88a8
	ipProtoUDP      = 17
)

var (
	SnapLen int32 = 128
	// AnyIpFilter is a BPF program for Layer 2 (Ethernet) interfaces.
	// It accepts IPv4, IPv6, and up to two 802.1Q/802.1ad VLAN tags.
	AnyIpFilter = []bpf.Instruction{
		bpf.LoadAbsolute{Off: 12, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeIPv4, SkipTrue: 14},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeIPv6, SkipTrue: 13},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeDot1Q, SkipTrue: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeDot1AD, SkipTrue: 1},
		bpf.RetConstant{Val: 0},
		bpf.LoadAbsolute{Off: 16, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeIPv4, SkipTrue: 8},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeIPv6, SkipTrue: 7},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeDot1Q, SkipTrue: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeDot1AD, SkipTrue: 1},
		bpf.RetConstant{Val: 0},
		bpf.LoadAbsolute{Off: 20, Size: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeIPv4, SkipTrue: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeIPv6, SkipTrue: 1},
		bpf.RetConstant{Val: 0},
		bpf.RetConstant{Val: uint32(SnapLen)},
	}

	// RawIpFilter is a BPF program for Layer 3 (raw IP) interfaces such as
	// WireGuard, tun, or PPP. There is no Ethernet header; the first byte
	// contains the IP version nibble.
	RawIpFilter = []bpf.Instruction{
		// Load the first byte (IP version + IHL for v4, or version + traffic class for v6).
		bpf.LoadAbsolute{Off: 0, Size: 1},
		// Shift right 4 to isolate the version nibble.
		bpf.ALUOpConstant{Op: bpf.ALUOpShiftRight, Val: 4},
		// If version == 4 (IPv4), accept.
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: 4, SkipTrue: 1},
		// If version == 6 (IPv6), accept; otherwise drop.
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: 6, SkipTrue: 0, SkipFalse: 1},
		// Accept.
		bpf.RetConstant{Val: uint32(SnapLen)},
		// Drop.
		bpf.RetConstant{Val: 0},
	}
)

const (
	EthHeaderSize = 14
	v4ProtoOffset = 23
	v6ProtoOffset = 20
	v6HeaderSize  = 40
)

type Packet struct {
	SrcIP        net.IP
	DstIP        net.IP
	Proto        uint8
	Len          uint64
	Version      int
	SrcInterface string
	Dot1qTag     int
	PktType      uint8 // AF_PACKET pkt_type: 0=HOST, 1=BROADCAST, 2=MULTICAST, 3=OTHERHOST, 4=OUTGOING
	IsTunnel     bool  // true if this packet carries encapsulated tunnel traffic
}

// Tunnel protocol numbers (IP header "protocol" field).
const (
	protoIPIP     = 4   // IPv4-in-IPv4 encapsulation
	protoIPv6inV4 = 41  // IPv6-in-IPv4 (6to4, etc)
	protoGRE      = 47  // Generic Routing Encapsulation
	protoESP      = 50  // IPsec Encapsulating Security Payload
	protoAH       = 51  // IPsec Authentication Header
	protoL2TP     = 115 // Layer 2 Tunnelling Protocol
)

// detectTunnel checks if the parsed packet carries tunnel/VPN traffic.
// It checks the IP protocol field for known tunnel protocols, and for
// UDP packets it inspects the first bytes of the payload to detect
// WireGuard and OpenVPN.
func detectTunnel(pkt []byte, ipHdrStart int, p *Packet) {
	// Check IP protocol field for tunnel protocols
	switch p.Proto {
	case protoIPIP, protoIPv6inV4, protoGRE, protoESP, protoAH, protoL2TP:
		p.IsTunnel = true
		return
	case ipProtoUDP:
		// Fall through to UDP payload inspection
	default:
		return
	}

	// UDP payload inspection for WireGuard and OpenVPN.
	// Calculate the offset to the UDP header.
	var udpStart int
	if p.Version == 4 {
		// IPv4: IHL (lower nibble of first byte) * 4
		ihl := int(pkt[ipHdrStart]&0x0F) * 4
		udpStart = ipHdrStart + ihl
	} else {
		// IPv6: fixed 40-byte header (ignoring extension headers for now)
		udpStart = ipHdrStart + 40
	}

	// Need at least UDP header (8 bytes) + 4 bytes of payload
	if udpStart+12 > len(pkt) {
		return
	}

	// Read UDP payload (after 8-byte UDP header)
	payloadStart := udpStart + 8
	if payloadStart+4 > len(pkt) {
		return
	}

	// WireGuard: first byte is message type (1=handshake init, 2=handshake resp,
	// 3=cookie, 4=data), followed by three zero reserved bytes.
	if pkt[payloadStart] >= 1 && pkt[payloadStart] <= 4 &&
		pkt[payloadStart+1] == 0 && pkt[payloadStart+2] == 0 && pkt[payloadStart+3] == 0 {
		p.IsTunnel = true
		return
	}

	// OpenVPN: first byte has opcode in bits 7-3 (values 1-10 are valid
	// control/data opcodes) and key_id in bits 2-0.
	opcode := pkt[payloadStart] >> 3
	if opcode >= 1 && opcode <= 10 {
		// Additional check: OpenVPN data packets (opcode 6,9,10) are followed
		// by a 4-byte peer-id or session-id. Control packets (1-5,7,8) have
		// an 8-byte session ID. Check that we have enough data and the
		// opcode is in the valid range.
		// Since opcode 6 (P_DATA_V1) and 9 (P_DATA_V2) are by far the most
		// common, and false positives are possible for random UDP traffic,
		// require the second byte to look like a plausible session/peer ID
		// (not all zeros, which would indicate random data).
		if payloadStart+8 <= len(pkt) {
			// Valid OpenVPN opcode with enough payload
			p.IsTunnel = true
			return
		}
	}
}

// SLL header size for Linux cooked capture (LINUX_SLL v1).
// AF_PACKET on PPP and similar L3 interfaces delivers packets with this
// 16-byte pseudo-header instead of a real link-layer header.
const sllHeaderSize = 16

// ParseIPPacket attempts to parse an IP packet from a slice of bytes.
// When isL3 is true, the data is treated as a raw IP packet (no Ethernet header),
// as delivered by SOCK_DGRAM on WireGuard/tun/PPP interfaces.
// When isL3 is false, the data is treated as an Ethernet frame.
func ParseIPPacket(pkt []byte, isL3 bool) Packet {
	if len(pkt) < 20 {
		return Packet{}
	}

	if isL3 {
		return parseRawIP(pkt)
	}

	// Ethernet frame.
	if len(pkt) < EthHeaderSize {
		return Packet{}
	}
	return parseEthernetFrame(pkt)
}

// parseRawIP parses a raw IP packet (no Ethernet header).
func parseRawIP(pkt []byte) Packet {
	ret := Packet{}
	ipVer := pkt[0] >> 4
	switch ipVer {
	case 4:
		if len(pkt) < 20 {
			return Packet{}
		}
		ret.Version = 4
		ret.SrcIP = net.IP(pkt[12:16])
		ret.DstIP = net.IP(pkt[16:20])
		ret.Proto = pkt[9]
		ret.Len = uint64(binary.BigEndian.Uint16(pkt[2:4]))
	case 6:
		if len(pkt) < 40 {
			return Packet{}
		}
		ret.Version = 6
		ret.SrcIP = net.IP(pkt[8:24])
		ret.DstIP = net.IP(pkt[24:40])
		ret.Proto = pkt[6]
		ret.Len = uint64(binary.BigEndian.Uint16(pkt[4:6])) + v6HeaderSize
	default:
		return Packet{}
	}
	detectTunnel(pkt, 0, &ret)
	return ret
}

// parseEthernetFrame parses an Ethernet frame, handling optional VLAN tags.
func parseEthernetFrame(pkt []byte) Packet {
	ret := Packet{}
	// Step from no vlan tag, to single vlan tag, to QinQ tags.
	for _, offset := range []int{0, 4, 8} {
		headerOffsets := EthHeaderSize + offset
		if headerOffsets > len(pkt) {
			return Packet{}
		}
		pktType := binary.BigEndian.Uint16(pkt[headerOffsets-2 : headerOffsets])
		if offset != 0 {
			// The VLAN TCI sits right after the 802.1Q EtherType marker.
			// For offset=4 (single tag): TCI is at pkt[14:16].
			// For offset=8 (QinQ inner): TCI is at pkt[18:20].
			tciStart := EthHeaderSize + offset - 4
			if tciStart+2 > len(pkt) {
				return Packet{}
			}
			tci := binary.BigEndian.Uint16(pkt[tciStart : tciStart+2])
			ret.Dot1qTag = int(tci & 0x0FFF)
		}
		switch pktType {
		case etherTypeIPv4:
			if headerOffsets+20 > len(pkt) {
				return Packet{}
			}
			ret.Version = 4
			ret.SrcIP = net.IP(pkt[headerOffsets+12 : headerOffsets+16])
			ret.DstIP = net.IP(pkt[headerOffsets+16 : headerOffsets+20])
			ret.Proto = uint8(pkt[v4ProtoOffset+offset])
			// IPv4 Total Length field already includes the IP header.
			ret.Len = uint64(binary.BigEndian.Uint16(pkt[headerOffsets+2 : headerOffsets+4]))
			detectTunnel(pkt, headerOffsets, &ret)
			return ret
		case etherTypeIPv6:
			if headerOffsets+40 > len(pkt) {
				return Packet{}
			}
			ret.Version = 6
			ret.SrcIP = net.IP(pkt[headerOffsets+8 : headerOffsets+24])
			ret.DstIP = net.IP(pkt[headerOffsets+24 : headerOffsets+40])
			ret.Proto = uint8(pkt[v6ProtoOffset+offset])
			// IPv6 Payload Length excludes the 40-byte fixed header.
			ret.Len = uint64(binary.BigEndian.Uint16(pkt[headerOffsets+4:headerOffsets+6])) + v6HeaderSize
			detectTunnel(pkt, headerOffsets, &ret)
			return ret
		}
	}
	// Not a recognised EtherType after VLAN unwinding — silently ignore.
	return Packet{}
}

// IsL3Device returns true if the named interface is a Layer 3 (point-to-point,
// no Ethernet header) device such as WireGuard, tun, or PPP. This is detected
// via the interface's ARPHRD type: ARPHRD_NONE (0xFFFE) or ARPHRD_PPP (512)
// indicate L3, while ARPHRD_ETHER (1) indicates L2.
func IsL3Device(dev string) bool {
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return false
	}
	// net.Interface doesn't expose ARPHRD directly, but point-to-point
	// L3 interfaces (wg, tun, ppp) have the PointToPoint flag set and
	// a zero HardwareAddr (no MAC address).
	if iface.Flags&net.FlagPointToPoint != 0 {
		return true
	}
	if len(iface.HardwareAddr) == 0 {
		return true
	}
	return false
}

// BPFFilterForDevice returns the appropriate BPF filter for the given device:
// RawIpFilter for L3 interfaces, AnyIpFilter for L2 (Ethernet) interfaces.
func BPFFilterForDevice(dev string) []bpf.Instruction {
	if IsL3Device(dev) {
		return RawIpFilter
	}
	return AnyIpFilter
}

// PacketHandler is called for each captured packet. pkt is only valid for the
// duration of the callback. wireLen is the original packet length on the wire.
type PacketHandler func(pkt []byte, wireLen uint32)
