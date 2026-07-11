//go:build linux

package packets

import (
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Ring is a TPACKET_V3 memory-mapped packet ring buffer.
// The kernel writes captured packets directly into shared memory;
// userspace reads them with zero copies and zero syscalls per packet.
type Ring struct {
	fd        int
	ring      []byte
	blockSize uint32
	blockNr   uint32
	blockIdx  uint32
	pollFd    unix.PollFd
	isL3      bool // true if the underlying interface is L3
}

// TPACKET_V3 struct layouts (mirrors linux/if_packet.h).
// These are naturally aligned and match the kernel layout on amd64 and arm64.

type tpacketReq3 struct {
	blockSize      uint32
	blockNr        uint32
	frameSize      uint32
	frameNr        uint32
	retireBlkTov   uint32
	sizeofPriv     uint32
	featureReqWord uint32
}

// blockDesc is the block descriptor at the start of each ring block.
// It embeds the tpacket_hdr_v1 fields inline.
type blockDesc struct {
	version      uint32
	offsetToPriv uint32
	// tpacket_hdr_v1 fields
	blockStatus   uint32
	numPkts       uint32
	offsetToFirst uint32
	blkLen        uint32
	seqNum        uint64
	tsFirstSec    uint32
	tsFirstNsec   uint32
	tsLastSec     uint32
	tsLastNsec    uint32
}

// tpacket3Hdr is the per-packet header inside a block.
type tpacket3Hdr struct {
	nextOffset uint32
	sec        uint32
	nsec       uint32
	snaplen    uint32
	pktLen     uint32
	status     uint32
	mac        uint16
	netw       uint16
	// hv1 variant
	rxhash   uint32
	vlanTCI  uint32
	vlanTPID uint16
	_pad1    uint16
	_pad2    [8]byte
}

const (
	tpStatusKernel = 0
	tpStatusUser   = 1 << 0

	// Ring sizing: 64 blocks of 256KB = 16MB total ring.
	// Each block holds ~170 packets at 1500 MTU snaplen.
	// retireBlkTov = 10ms means blocks are retired even if not full,
	// giving sub-10ms latency for low-rate traffic.
	defaultBlockSize = 1 << 18 // 256 KiB
	defaultBlockNr   = 64
	defaultFrameSize = 1 << 11 // 2048 (must be >= snaplen + headers)
	defaultBlkTov    = 10      // ms
)

// NewRing creates a TPACKET_V3 mmap'd ring buffer for the given interface.
// It replaces FetchPcapSock + epoll/Read for high-performance capture.
func NewRing(dev string, promisc bool) (*Ring, error) {
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return nil, fmt.Errorf("ring: interface %s: %w", dev, err)
	}

	l3 := IsL3Device(dev)
	// Always use SOCK_DGRAM: we read from tp_net (IP header) not tp_mac,
	// so we don't need the Ethernet header. SOCK_DGRAM also ensures the
	// BPF filter operates on the IP header directly.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return nil, fmt.Errorf("ring: socket: %w", err)
	}

	// Set TPACKET version to V3
	if err := unix.SetsockoptInt(fd, unix.SOL_PACKET, unix.PACKET_VERSION, unix.TPACKET_V3); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("ring: PACKET_VERSION: %w", err)
	}

	// Configure the RX ring
	blockSize := uint32(defaultBlockSize)
	blockNr := uint32(defaultBlockNr)
	frameSize := uint32(defaultFrameSize)

	req := tpacketReq3{
		blockSize:    blockSize,
		blockNr:      blockNr,
		frameSize:    frameSize,
		frameNr:      (blockSize * blockNr) / frameSize,
		retireBlkTov: defaultBlkTov,
	}

	_, _, errno := unix.Syscall6(
		unix.SYS_SETSOCKOPT,
		uintptr(fd),
		unix.SOL_PACKET,
		unix.PACKET_RX_RING,
		uintptr(unsafe.Pointer(&req)),
		unsafe.Sizeof(req),
		0,
	)
	if errno != 0 {
		unix.Close(fd)
		return nil, fmt.Errorf("ring: PACKET_RX_RING: %w", errno)
	}

	// mmap the ring buffer
	totalSize := int(blockSize * blockNr)
	ring, err := unix.Mmap(fd, 0, totalSize,
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_SHARED|unix.MAP_LOCKED)
	if err != nil {
		// Retry without MAP_LOCKED (requires CAP_IPC_LOCK)
		ring, err = unix.Mmap(fd, 0, totalSize,
			unix.PROT_READ|unix.PROT_WRITE,
			unix.MAP_SHARED)
		if err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("ring: mmap: %w", err)
		}
	}

	// Bind to interface
	sa := &unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ALL),
		Ifindex:  iface.Index,
	}
	if err := unix.Bind(fd, sa); err != nil {
		unix.Munmap(ring)
		unix.Close(fd)
		return nil, fmt.Errorf("ring: bind %s: %w", dev, err)
	}

	// Promiscuous mode
	if promisc {
		mreq := unix.PacketMreq{
			Ifindex: int32(iface.Index),
			Type:    unix.PACKET_MR_PROMISC,
		}
		if err := unix.SetsockoptPacketMreq(fd, unix.SOL_PACKET, unix.PACKET_ADD_MEMBERSHIP, &mreq); err != nil {
			unix.Munmap(ring)
			unix.Close(fd)
			return nil, fmt.Errorf("ring: promisc %s: %w", dev, err)
		}
	}

	// Apply BPF filter — always RawIpFilter since SOCK_DGRAM delivers raw IP
	if err := ApplyBPFFilter(fd, RawIpFilter); err != nil {
		unix.Munmap(ring)
		unix.Close(fd)
		return nil, fmt.Errorf("ring: BPF %s: %w", dev, err)
	}

	return &Ring{
		fd:        fd,
		ring:      ring,
		blockSize: blockSize,
		blockNr:   blockNr,
		pollFd:    unix.PollFd{Fd: int32(fd), Events: unix.POLLIN | unix.POLLERR},
		isL3:      l3,
	}, nil
}

// Close releases the ring buffer and closes the socket.
func (r *Ring) Close() {
	if r.ring != nil {
		unix.Munmap(r.ring)
		r.ring = nil
	}
	if r.fd >= 0 {
		unix.Close(r.fd)
		r.fd = -1
	}
}

// IsL3 returns whether this ring captures from an L3 interface.
func (r *Ring) IsL3() bool {
	return r.isL3
}

// ReadBlock polls for the next ready block, walks all packets in it,
// calls handler for each, then releases the block back to the kernel.
// Returns false if the poll timed out (no packets).
// The timeout is in milliseconds; -1 blocks forever.
func (r *Ring) ReadBlock(handler PacketHandler, timeoutMs int) (bool, error) {
	offset := uintptr(r.blockIdx) * uintptr(r.blockSize)
	bd := (*blockDesc)(unsafe.Pointer(&r.ring[offset]))

	// Check if block is ready (user-owned)
	status := atomic.LoadUint32(&bd.blockStatus)
	if status&tpStatusUser == 0 {
		// Block not ready — poll
		if _, err := unix.Poll([]unix.PollFd{r.pollFd}, timeoutMs); err != nil {
			if normalizeLinuxPollError(err) == nil {
				return false, nil
			}
			return false, fmt.Errorf("ring: poll: %w", err)
		}
		status = atomic.LoadUint32(&bd.blockStatus)
		if status&tpStatusUser == 0 {
			return false, nil // timeout
		}
	}

	// Walk packets in this block
	numPkts := bd.numPkts
	pktOff := offset + uintptr(bd.offsetToFirst)

	for i := uint32(0); i < numPkts; i++ {
		if int(pktOff)+int(unsafe.Sizeof(tpacket3Hdr{})) > len(r.ring) {
			break
		}
		hdr := (*tpacket3Hdr)(unsafe.Pointer(&r.ring[pktOff]))

		// Always use tp_net to get the network (IP) header directly.
		// On L2, tp_mac points to the Ethernet header but can be unreliable
		// when VLAN tags are stripped. tp_net always points to the IP header.
		dataOff := hdr.netw
		snapLen := hdr.snaplen
		dataStart := pktOff + uintptr(dataOff)
		dataEnd := dataStart + uintptr(snapLen)
		if int(dataEnd) > len(r.ring) {
			break
		}

		// Data from tp_net is always the raw IP header, regardless of L2/L3.
		handler(r.ring[dataStart:dataEnd], hdr.pktLen)

		if hdr.nextOffset != 0 {
			pktOff += uintptr(hdr.nextOffset)
		} else {
			break
		}
	}

	// Release block back to kernel
	atomic.StoreUint32(&bd.blockStatus, tpStatusKernel)

	r.blockIdx = (r.blockIdx + 1) % r.blockNr
	return true, nil
}

func normalizeLinuxPollError(err error) error {
	if errors.Is(err, unix.EINTR) {
		return nil
	}
	return err
}
