//go:build linux

package packets

import (
	"fmt"
	"log"
	"net"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

// FetchPcapSock creates a new AF_PACKET socket for capturing traffic on the
// given interface. When promisc is true it enables PACKET_MR_PROMISC so that
// the NIC delivers all frames (required for SPAN/mirror ports).
//
// For L3 interfaces, SOCK_DGRAM strips the link-layer header and delivers the
// IP payload directly.
func FetchPcapSock(dev string, promisc bool) (int, error) {
	protocol := uint16(unix.ETH_P_ALL)
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return -1, err
	}
	addr := &unix.SockaddrLinklayer{
		Protocol: uint16(htons(unix.ETH_P_ALL)),
		Ifindex:  iface.Index,
	}
	sockType := unix.SOCK_RAW
	if IsL3Device(dev) {
		sockType = unix.SOCK_DGRAM
	}
	fd, err := unix.Socket(unix.AF_PACKET, sockType, int(htons(protocol)))
	if err != nil {
		return -1, err
	}
	_ = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, 4*1024*1024)
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return -1, err
	}
	if promisc {
		mreq := unix.PacketMreq{
			Ifindex: int32(iface.Index),
			Type:    unix.PACKET_MR_PROMISC,
		}
		if err := unix.SetsockoptPacketMreq(fd, unix.SOL_PACKET, unix.PACKET_ADD_MEMBERSHIP, &mreq); err != nil {
			_ = unix.Close(fd)
			return -1, fmt.Errorf("enable promiscuous mode on %s: %w", dev, err)
		}
	}
	return fd, nil
}

// ApplyBPFFilter applies the given BPF filter to the given socket descriptor.
func ApplyBPFFilter(sockFd int, rawBpfFilter []bpf.Instruction) error {
	expr, err := bpf.Assemble(rawBpfFilter)
	if err != nil {
		log.Printf("packets: BPF assemble failed: %v", err)
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
