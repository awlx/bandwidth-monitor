//go:build darwin

package packets

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"runtime"
	"unsafe"

	"golang.org/x/net/bpf"
	"golang.org/x/sys/unix"
)

const (
	bpfDeviceLimit       = 256
	bpfRequestedBuffer   = 1 << 20
	bpfMaximumBuffer     = 16 << 20
	bpfReadTimeoutMillis = 100
	minBPFHeaderLen      = 18
)

type bpfSystem struct {
	open        func(string, int, uint32) (int, error)
	close       func(int) error
	read        func(int, []byte) (int, error)
	poll        func([]unix.PollFd, int) (int, error)
	ioctlGetInt func(int, uint) (int, error)
	ioctlSetInt func(int, uint, int) error
	ioctlPtr    func(int, uint, unsafe.Pointer) error
	ioctlNoArg  func(int, uint) error
}

var nativeBPFSystem = bpfSystem{
	open:        unix.Open,
	close:       unix.Close,
	read:        unix.Read,
	poll:        unix.Poll,
	ioctlGetInt: unix.IoctlGetInt,
	ioctlSetInt: unix.IoctlSetInt,
	ioctlPtr:    ioctlPointer,
	ioctlNoArg:  ioctlWithoutArgument,
}

// Ring is a Berkeley Packet Filter capture buffer on Darwin.
type Ring struct {
	fd       int
	buffer   []byte
	linkType int
	system   bpfSystem
}

// NewRing opens an available BPF device and binds it to dev.
func NewRing(dev string, promisc bool) (*Ring, error) {
	iface, err := net.InterfaceByName(dev)
	if err != nil {
		return nil, fmt.Errorf("BPF interface %q: %w", dev, err)
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("BPF interface %q is down", dev)
	}
	return newDarwinRing(iface.Name, promisc, nativeBPFSystem)
}

func newDarwinRing(dev string, promisc bool, system bpfSystem) (_ *Ring, err error) {
	fd, err := openBPFDevice(system.open)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = system.close(fd)
		}
	}()

	if err = system.ioctlSetInt(fd, unix.BIOCSBLEN, bpfRequestedBuffer); err != nil {
		return nil, fmt.Errorf("configure BPF buffer length: %w", err)
	}
	if err = bindBPFInterface(fd, dev, system.ioctlPtr); err != nil {
		return nil, err
	}
	if err = system.ioctlSetInt(fd, unix.BIOCIMMEDIATE, 1); err != nil {
		return nil, fmt.Errorf("enable BPF immediate mode: %w", err)
	}
	timeout := unix.NsecToTimeval(int64(bpfReadTimeoutMillis) * 1e6)
	if err = system.ioctlPtr(fd, unix.BIOCSRTIMEOUT, unsafe.Pointer(&timeout)); err != nil {
		return nil, fmt.Errorf("configure BPF read timeout: %w", err)
	}
	runtime.KeepAlive(timeout)

	bufferLength, err := system.ioctlGetInt(fd, unix.BIOCGBLEN)
	if err != nil {
		return nil, fmt.Errorf("read BPF buffer length: %w", err)
	}
	if bufferLength <= 0 || bufferLength > bpfMaximumBuffer {
		return nil, fmt.Errorf("invalid BPF buffer length %d", bufferLength)
	}
	linkType, err := system.ioctlGetInt(fd, unix.BIOCGDLT)
	if err != nil {
		return nil, fmt.Errorf("read BPF link type: %w", err)
	}
	if !supportedDarwinLinkType(linkType) {
		return nil, fmt.Errorf(
			"unsupported BPF link type %d (supported: Ethernet, null/loopback, raw IP)",
			linkType,
		)
	}
	if filter := filterForDarwinLinkType(linkType); filter != nil {
		if err = applyDarwinBPFFilter(fd, filter, system.ioctlPtr); err != nil {
			return nil, fmt.Errorf("install BPF IP filter: %w", err)
		}
	}
	if promisc {
		if err = system.ioctlNoArg(fd, unix.BIOCPROMISC); err != nil {
			return nil, fmt.Errorf("enable BPF promiscuous mode: %w", err)
		}
	}

	return &Ring{
		fd:       fd,
		buffer:   make([]byte, bufferLength),
		linkType: linkType,
		system:   system,
	}, nil
}

func openBPFDevice(open func(string, int, uint32) (int, error)) (int, error) {
	paths := make([]string, 0, bpfDeviceLimit+1)
	paths = append(paths, "/dev/bpf")
	for index := 0; index < bpfDeviceLimit; index++ {
		paths = append(paths, fmt.Sprintf("/dev/bpf%d", index))
	}
	sawBusy := false
	for _, path := range paths {
		fd, err := open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
		if err == nil {
			return fd, nil
		}
		switch {
		case errors.Is(err, unix.EBUSY):
			sawBusy = true
		case errors.Is(err, unix.ENOENT):
			continue
		case errors.Is(err, unix.EACCES), errors.Is(err, unix.EPERM):
			return -1, fmt.Errorf(
				"open BPF device: %w (macOS typically requires root or access to /dev/bpf*)",
				err,
			)
		default:
			return -1, fmt.Errorf("open BPF device: %w", err)
		}
	}
	if sawBusy {
		return -1, errors.New("open BPF device: all devices are busy")
	}
	return -1, errors.New("open BPF device: no /dev/bpf devices found")
}

func bindBPFInterface(
	fd int,
	name string,
	ioctl func(int, uint, unsafe.Pointer) error,
) error {
	if name == "" || len(name) >= unix.IFNAMSIZ {
		return fmt.Errorf("invalid BPF interface name %q", name)
	}
	var request [32]byte
	copy(request[:unix.IFNAMSIZ-1], name)
	if err := ioctl(fd, unix.BIOCSETIF, unsafe.Pointer(&request[0])); err != nil {
		return fmt.Errorf("bind BPF interface %q: %w", name, err)
	}
	runtime.KeepAlive(request)
	return nil
}

func supportedDarwinLinkType(linkType int) bool {
	switch linkType {
	case unix.DLT_EN10MB, unix.DLT_NULL, unix.DLT_LOOP, unix.DLT_RAW:
		return true
	default:
		return false
	}
}

func filterForDarwinLinkType(linkType int) []bpf.Instruction {
	switch linkType {
	case unix.DLT_EN10MB:
		return AnyIpFilter
	case unix.DLT_RAW:
		return RawIpFilter
	default:
		return nil
	}
}

func applyDarwinBPFFilter(
	fd int,
	instructions []bpf.Instruction,
	ioctl func(int, uint, unsafe.Pointer) error,
) error {
	raw, err := bpf.Assemble(instructions)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return errors.New("empty BPF program")
	}
	native := make([]unix.BpfInsn, len(raw))
	for index, instruction := range raw {
		native[index] = unix.BpfInsn{
			Code: instruction.Op,
			Jt:   instruction.Jt,
			Jf:   instruction.Jf,
			K:    instruction.K,
		}
	}
	program := unix.BpfProgram{Len: uint32(len(native)), Insns: &native[0]}
	if err := ioctl(fd, unix.BIOCSETF, unsafe.Pointer(&program)); err != nil {
		return err
	}
	runtime.KeepAlive(native)
	runtime.KeepAlive(program)
	return nil
}

func ioctlPointer(fd int, request uint, argument unsafe.Pointer) error {
	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(request),
		uintptr(argument),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func ioctlWithoutArgument(fd int, request uint) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), 0)
	if errno != 0 {
		return errno
	}
	return nil
}

// Close releases the BPF descriptor. It is safe to call more than once.
func (r *Ring) Close() {
	if r.fd >= 0 {
		_ = r.system.close(r.fd)
		r.fd = -1
	}
	r.buffer = nil
}

// IsL3 reports true because the Darwin backend normalizes supported link-layer
// records to raw IPv4 or IPv6 before invoking PacketHandler.
func (r *Ring) IsL3() bool {
	return true
}

// ReadBlock waits for BPF records, validates and aligns them, and invokes the
// handler with normalized raw IP packets.
func (r *Ring) ReadBlock(handler PacketHandler, timeoutMs int) (bool, error) {
	if r.fd < 0 {
		return false, errors.New("BPF capture is closed")
	}
	pollFDs := []unix.PollFd{{Fd: int32(r.fd), Events: unix.POLLIN | unix.POLLERR}}
	ready, err := r.system.poll(pollFDs, timeoutMs)
	if errors.Is(err, unix.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("poll BPF device: %w", err)
	}
	if ready == 0 {
		return false, nil
	}
	if pollFDs[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
		return false, fmt.Errorf("BPF device poll failed (events %#x)", pollFDs[0].Revents)
	}
	bytesRead, err := r.system.read(r.fd, r.buffer)
	if errors.Is(err, unix.EINTR) || errors.Is(err, unix.EAGAIN) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read BPF device: %w", err)
	}
	if bytesRead == 0 {
		return false, nil
	}
	if bytesRead < 0 || bytesRead > len(r.buffer) {
		return false, fmt.Errorf("invalid BPF read length %d", bytesRead)
	}
	count, err := parseBPFRecords(r.buffer[:bytesRead], r.linkType, handler)
	if err != nil {
		return false, fmt.Errorf("parse BPF records: %w", err)
	}
	return count > 0, nil
}

func parseBPFRecords(data []byte, linkType int, handler PacketHandler) (int, error) {
	accepted := 0
	for offset := 0; offset < len(data); {
		remaining := len(data) - offset
		if remaining < minBPFHeaderLen {
			return accepted, fmt.Errorf("truncated BPF header at offset %d", offset)
		}
		header := data[offset:]
		capturedLength := binary.NativeEndian.Uint32(header[8:12])
		wireLength := binary.NativeEndian.Uint32(header[12:16])
		headerLength := binary.NativeEndian.Uint16(header[16:18])
		if headerLength < minBPFHeaderLen {
			return accepted, fmt.Errorf("invalid BPF header length %d at offset %d", headerLength, offset)
		}
		if wireLength < capturedLength {
			return accepted, fmt.Errorf(
				"BPF wire length %d is smaller than captured length %d at offset %d",
				wireLength, capturedLength, offset,
			)
		}
		recordLength := uint64(headerLength) + uint64(capturedLength)
		if recordLength > uint64(remaining) {
			return accepted, fmt.Errorf("truncated BPF record at offset %d", offset)
		}
		recordEnd := offset + int(recordLength)
		payload, isIP, err := darwinIPPayload(data[offset+int(headerLength):recordEnd], linkType)
		if err != nil {
			return accepted, fmt.Errorf("invalid BPF payload at offset %d: %w", offset, err)
		}
		if isIP {
			handler(payload, wireLength)
			accepted++
		}

		alignedLength := bpfWordAlign(int(recordLength))
		next := offset + alignedLength
		if next > len(data) {
			if recordEnd == len(data) {
				return accepted, nil
			}
			return accepted, fmt.Errorf("truncated BPF record alignment at offset %d", offset)
		}
		if next <= offset {
			return accepted, fmt.Errorf("non-advancing BPF record at offset %d", offset)
		}
		offset = next
	}
	return accepted, nil
}

func bpfWordAlign(length int) int {
	alignment := int(unix.BPF_ALIGNMENT)
	return (length + alignment - 1) &^ (alignment - 1)
}

func darwinIPPayload(frame []byte, linkType int) ([]byte, bool, error) {
	switch linkType {
	case unix.DLT_EN10MB:
		return ethernetIPPayload(frame)
	case unix.DLT_NULL, unix.DLT_LOOP:
		if len(frame) < 4 {
			return nil, false, errors.New("truncated loopback header")
		}
		var order binary.ByteOrder = binary.NativeEndian
		if linkType == unix.DLT_LOOP {
			order = binary.BigEndian
		}
		family := order.Uint32(frame[:4])
		if family != unix.AF_INET && family != unix.AF_INET6 {
			return nil, false, nil
		}
		return rawIPPayload(frame[4:])
	case unix.DLT_RAW:
		return rawIPPayload(frame)
	default:
		return nil, false, fmt.Errorf("unsupported link type %d", linkType)
	}
}

func ethernetIPPayload(frame []byte) ([]byte, bool, error) {
	if len(frame) < EthHeaderSize {
		return nil, false, errors.New("truncated Ethernet header")
	}
	etherType := binary.BigEndian.Uint16(frame[12:14])
	offset := EthHeaderSize
	for tags := 0; tags < 2 && (etherType == etherTypeDot1Q || etherType == etherTypeDot1AD); tags++ {
		if offset+4 > len(frame) {
			return nil, false, errors.New("truncated VLAN header")
		}
		etherType = binary.BigEndian.Uint16(frame[offset+2 : offset+4])
		offset += 4
	}
	if etherType != etherTypeIPv4 && etherType != etherTypeIPv6 {
		return nil, false, nil
	}
	return rawIPPayload(frame[offset:])
}

func rawIPPayload(packet []byte) ([]byte, bool, error) {
	if len(packet) == 0 {
		return nil, false, errors.New("empty IP packet")
	}
	switch packet[0] >> 4 {
	case 4:
		if len(packet) < 20 {
			return nil, false, errors.New("truncated IPv4 header")
		}
	case 6:
		if len(packet) < 40 {
			return nil, false, errors.New("truncated IPv6 header")
		}
	default:
		return nil, false, nil
	}
	return packet, true, nil
}
