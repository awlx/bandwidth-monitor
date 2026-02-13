package packets

import (
	"encoding/binary"
	"log"
	"net"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

var (
	SnapLen int32 = 128
	// Setup BPF filter here to capture ipv4/ipv6 traffic.
	AnyIpFilter = []bpf.Instruction{
		// 12 bytes is offset for frame type, which is 2 bytes long.
		bpf.LoadAbsolute{Off: 12, Size: 2},
		// Snag Ether type IPv4/v6
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.ETH_P_IP, SkipTrue: 2},
		bpf.JumpIf{Cond: bpf.JumpEqual, Val: unix.ETH_P_IPV6, SkipTrue: 1},
		bpf.RetConstant{Val: 0},
		// Return snaplen
		bpf.RetConstant{Val: uint32(SnapLen)},
	}
)

type Packet struct {
	SrcIP net.IP
	DstIP net.IP
	Proto uint8
	Len   uint64
}

// ParseIPPacket attempts to parse an IP packet from a slice of bytes.
func ParseIPPacket(pkt []byte) Packet {
	if len(pkt) < 48 {
		return Packet{}
	}
	ret := Packet{}
	pktType := binary.BigEndian.Uint16(pkt[12:14])
	switch pktType {
	case unix.ETH_P_IP:
		// Src IP is the range of 16 to 20 bytes into IP header, which starts 14 bytes into ethernet header
		ret.SrcIP = net.IP(pkt[14+16 : 14+20])
		ret.DstIP = net.IP(pkt[14+20 : 14+24])
		ret.Proto = uint8(pkt[23])
		ret.Len = uint64(binary.BigEndian.Uint16(pkt[14+2 : 14+4]))
		return ret
	case unix.ETH_P_IPV6:
		// Src IP is the range of 8 to 24 bytes into IP header, which starts 14 bytes into ethernet header
		ret.SrcIP = net.IP(pkt[14+8 : 14+24])
		ret.DstIP = net.IP(pkt[14+24 : 14+40])
		ret.Proto = uint8(pkt[20])
		ret.Len = uint64(binary.BigEndian.Uint16(pkt[14+4 : 14+6]))
		return ret
	default:
		log.Printf("Unknown packet \n")
	}
	return ret
}

// FetchPcapSock creates a new socket for capturing traffic on.
func FetchPcapSock(dev string) (int, error) {
	protocol := uint16(unix.ETH_P_ALL)
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return -1, err
	}
	addr := &unix.SockaddrLinklayer{
		Protocol: uint16(htons(unix.ETH_P_ALL)),
		Ifindex:  iface.Index,
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(protocol)))
	if err != nil {
		return -1, err
	}
	if err := unix.Bind(fd, addr); err != nil {
		return -1, err
	}
	return fd, nil
}

// ApplyBPFFilter applies the given BPF filter to the given socket descriptor.
func ApplyBPFFilter(sockFd int, rawBpfFilter []bpf.Instruction) error {
	expr, err := bpf.Assemble(rawBpfFilter)
	if err != nil {
		log.Printf("failed attachment %s \n", err)
		return err
	}
	prog := &unix.SockFprog{
		Len:    uint16(len(expr)),
		Filter: (*unix.SockFilter)(unsafe.Pointer(&expr[0])),
	}
	return unix.SetsockoptSockFprog(sockFd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, prog)
}

func htons(i uint16) uint16 {
	return (i<<8)&0xff00 | i>>8
}

func parseCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}
