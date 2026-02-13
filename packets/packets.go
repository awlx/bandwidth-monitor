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
}

func extractUint16(a uint32, offset, n uint) uint16 {
	return uint16((a >> offset) & (1<<n - 1))
}

// ParseIPPacket attempts to parse an IP packet from a slice of bytes.
func ParseIPPacket(pkt []byte) Packet {
	if len(pkt) < 48 {
		return Packet{}
	}
	ret := Packet{}
	// Step from no vlan tag, to single vlan tag, to QinQ tags.
	for _, offset := range []int{0, 4, 8} {
		headerOffsets := EthHeaderSize + offset
		pktType := binary.BigEndian.Uint16(pkt[headerOffsets-2 : headerOffsets])
		if offset != 0 {
			dot1QTag := pkt[12+offset : 16+offset]
			// Take the last 12 bits from
			ret.Dot1qTag = int(extractUint16(binary.BigEndian.Uint32(dot1QTag), 22, 12))
		}
		switch pktType {
		case unix.ETH_P_IP:
			ret.Version = 4
			headerSize := pkt[headerOffsets : headerOffsets+2]
			headerSizeBits := uint64(extractUint16(uint32(binary.BigEndian.Uint16(headerSize)), 8, 4) * 8)
			// Src IP is the range of 16 to 20 bytes into IP header, which starts 14 bytes into ethernet header
			ret.SrcIP = net.IP(pkt[headerOffsets+12 : headerOffsets+16])
			ret.DstIP = net.IP(pkt[headerOffsets+16 : headerOffsets+20])
			ret.Proto = uint8(pkt[v4ProtoOffset+offset])
			ret.Len = uint64(binary.BigEndian.Uint16(pkt[headerOffsets+2:headerOffsets+4])) + uint64(headerOffsets) + headerSizeBits
			return ret
		case unix.ETH_P_IPV6:
			ret.Version = 6
			// Src IP is the range of 8 to 24 bytes into IP header, which starts 14 bytes into ethernet header
			ret.SrcIP = net.IP(pkt[headerOffsets+8 : headerOffsets+24])
			ret.DstIP = net.IP(pkt[headerOffsets+24 : headerOffsets+40])
			ret.Proto = uint8(pkt[v6ProtoOffset+offset])
			// Include the header length AND ethernet header size itself in the calculation.
			ret.Len = uint64(binary.BigEndian.Uint16(pkt[headerOffsets+4:headerOffsets+6])) + uint64(headerOffsets) + v6HeaderSize
			return ret
		}
	}
	// If we fall through to here, we have junk data.
	log.Printf("Unknown packet \n")
	return Packet{}
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
		_ = unix.Close(fd)
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

func CreateEpoller(sockFD int) (int, error) {
	unix.SetNonblock(sockFD, true)
	epfd, err := unix.EpollCreate(20)
	if err != nil {
		return -1, err
	}
	event := unix.EpollEvent{
		Events: unix.EPOLLIN,
		Fd:     int32(sockFD),
	}
	if err := unix.EpollCtl(epfd, unix.EPOLL_CTL_ADD, sockFD, &event); err != nil {
		unix.Close(epfd)
		return -1, err
	}
	return epfd, nil
}

func htons(i uint16) uint16 {
	return (i<<8)&0xff00 | i>>8
}

func parseCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}
