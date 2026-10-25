// Package bsdnet reads interface counters and routes from the FreeBSD kernel
// routing socket, standing in for rtnetlink on OPNsense/pfSense hosts.
package bsdnet

import "encoding/binary"

// Layout of FreeBSD (11+) struct if_msghdr followed by struct if_data, as
// returned by sysctl NET_RT_IFLIST. golang.org/x/sys still describes the
// pre-FreeBSD 11 if_data, so the records are decoded by hand.
const (
	rtmIfInfo = 0x0e

	ifmFlagsOffset = 8
	ifmIndexOffset = 12
	ifmDataOffset  = 16

	ifdTypeOffset      = 0
	ifdLinkStateOffset = 4
	ifdDatalenOffset   = 6
	ifdMTUOffset       = 8
	ifdBaudrateOffset  = 16
	ifdIPacketsOffset  = 24
	ifdIErrorsOffset   = 32
	ifdOPacketsOffset  = 40
	ifdOErrorsOffset   = 48
	ifdIBytesOffset    = 64
	ifdOBytesOffset    = 72
	ifdIQDropsOffset   = 96
	ifdOQDropsOffset   = 104
	minIfDataLen       = 112
)

// Interface flag and link-state values from <net/if.h>.
const (
	FlagUp      = 0x1
	FlagRunning = 0x40

	LinkStateUnknown = 0
	LinkStateDown    = 1
	LinkStateUp      = 2
)

// Interface type values from <net/if_types.h> used for classification.
const (
	TypeEther     = 0x06
	TypePPP       = 0x17
	TypeLoop      = 0x18
	TypeL2VLAN    = 0x87
	TypeGIF       = 0xf0
	TypeEnc       = 0xf4
	TypePflog     = 0xf6
	TypePfsync    = 0xf7
	TypeWireGuard = 0xf8
)

// IfData is one interface's state and counters.
type IfData struct {
	Index     int
	Name      string
	Type      uint8
	Flags     uint32
	LinkState uint8
	MTU       uint32
	Baudrate  uint64 // bits per second, 0 when unknown

	RxPackets, TxPackets uint64
	RxBytes, TxBytes     uint64
	RxErrors, TxErrors   uint64
	RxDropped, TxDropped uint64
}

// ParseIfList decodes the RTM_IFINFO records of a NET_RT_IFLIST dump.
// Address records and truncated or unknown messages are skipped.
func ParseIfList(b []byte) []IfData {
	ne := binary.NativeEndian
	var out []IfData
	for len(b) >= 4 {
		msgLen := int(ne.Uint16(b[0:2]))
		if msgLen < 4 || msgLen > len(b) {
			break
		}
		msg := b[:msgLen]
		b = b[msgLen:]
		if msg[3] != rtmIfInfo || len(msg) < ifmDataOffset+minIfDataLen {
			continue
		}
		d := msg[ifmDataOffset:]
		if int(ne.Uint16(d[ifdDatalenOffset:])) < minIfDataLen {
			continue
		}
		out = append(out, IfData{
			Index:     int(ne.Uint16(msg[ifmIndexOffset:])),
			Flags:     ne.Uint32(msg[ifmFlagsOffset:]),
			Type:      d[ifdTypeOffset],
			LinkState: d[ifdLinkStateOffset],
			MTU:       ne.Uint32(d[ifdMTUOffset:]),
			Baudrate:  ne.Uint64(d[ifdBaudrateOffset:]),
			RxPackets: ne.Uint64(d[ifdIPacketsOffset:]),
			RxErrors:  ne.Uint64(d[ifdIErrorsOffset:]),
			TxPackets: ne.Uint64(d[ifdOPacketsOffset:]),
			TxErrors:  ne.Uint64(d[ifdOErrorsOffset:]),
			RxBytes:   ne.Uint64(d[ifdIBytesOffset:]),
			TxBytes:   ne.Uint64(d[ifdOBytesOffset:]),
			RxDropped: ne.Uint64(d[ifdIQDropsOffset:]),
			TxDropped: ne.Uint64(d[ifdOQDropsOffset:]),
		})
	}
	return out
}

// OperState maps interface flags and link state to the Linux-style
// operational state strings the API reports.
func (d IfData) OperState() string {
	if d.Flags&FlagUp == 0 {
		return "down"
	}
	switch d.LinkState {
	case LinkStateUp:
		return "up"
	case LinkStateDown:
		return "down"
	}
	if d.Flags&FlagRunning != 0 {
		return "unknown"
	}
	return "down"
}
