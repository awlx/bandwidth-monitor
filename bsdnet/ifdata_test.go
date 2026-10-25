package bsdnet

import (
	"encoding/binary"
	"testing"
)

func ifInfoMsg(index uint16, flags uint32, typ, linkState uint8, mtu uint32, baud, ibytes, obytes uint64) []byte {
	ne := binary.NativeEndian
	msg := make([]byte, ifmDataOffset+152) // FreeBSD 14 if_data is 152 bytes
	ne.PutUint16(msg[0:], uint16(len(msg)))
	msg[2] = 5 // RTM_VERSION
	msg[3] = rtmIfInfo
	ne.PutUint32(msg[ifmFlagsOffset:], flags)
	ne.PutUint16(msg[ifmIndexOffset:], index)
	d := msg[ifmDataOffset:]
	d[ifdTypeOffset] = typ
	d[ifdLinkStateOffset] = linkState
	ne.PutUint16(d[ifdDatalenOffset:], 152)
	ne.PutUint32(d[ifdMTUOffset:], mtu)
	ne.PutUint64(d[ifdBaudrateOffset:], baud)
	ne.PutUint64(d[ifdIPacketsOffset:], 10)
	ne.PutUint64(d[ifdIErrorsOffset:], 1)
	ne.PutUint64(d[ifdOPacketsOffset:], 20)
	ne.PutUint64(d[ifdOErrorsOffset:], 2)
	ne.PutUint64(d[ifdIBytesOffset:], ibytes)
	ne.PutUint64(d[ifdOBytesOffset:], obytes)
	ne.PutUint64(d[ifdIQDropsOffset:], 3)
	ne.PutUint64(d[ifdOQDropsOffset:], 4)
	return msg
}

func TestParseIfList(t *testing.T) {
	// RTM_NEWADDR record that must be skipped.
	addr := make([]byte, 20)
	binary.NativeEndian.PutUint16(addr, 20)
	addr[3] = 0x0c

	var b []byte
	b = append(b, ifInfoMsg(1, FlagUp|FlagRunning, TypeEther, LinkStateUp, 1500, 1_000_000_000, 1000, 2000)...)
	b = append(b, addr...)
	b = append(b, ifInfoMsg(7, FlagUp|FlagRunning, TypeWireGuard, LinkStateUnknown, 1420, 0, 5, 6)...)
	b = append(b, 0xff, 0xff) // truncated trailer

	got := ParseIfList(b)
	if len(got) != 2 {
		t.Fatalf("got %d interfaces, want 2", len(got))
	}
	e := got[0]
	if e.Index != 1 || e.Type != TypeEther || e.MTU != 1500 || e.Baudrate != 1_000_000_000 ||
		e.RxBytes != 1000 || e.TxBytes != 2000 || e.RxPackets != 10 || e.TxPackets != 20 ||
		e.RxErrors != 1 || e.TxErrors != 2 || e.RxDropped != 3 || e.TxDropped != 4 {
		t.Errorf("ether = %+v", e)
	}
	if e.OperState() != "up" {
		t.Errorf("ether oper = %q", e.OperState())
	}
	if got[1].Index != 7 || got[1].Type != TypeWireGuard || got[1].OperState() != "unknown" {
		t.Errorf("wg = %+v oper=%q", got[1], got[1].OperState())
	}
}

func TestParseIfListRejectsShortIfData(t *testing.T) {
	m := ifInfoMsg(1, FlagUp, TypeEther, LinkStateUp, 1500, 0, 0, 0)
	binary.NativeEndian.PutUint16(m[ifmDataOffset+ifdDatalenOffset:], 80) // pre-FreeBSD 11 layout
	if got := ParseIfList(m); len(got) != 0 {
		t.Errorf("expected short if_data to be skipped, got %+v", got)
	}
}

func TestOperState(t *testing.T) {
	cases := []struct {
		d    IfData
		want string
	}{
		{IfData{Flags: 0, LinkState: LinkStateUp}, "down"},
		{IfData{Flags: FlagUp, LinkState: LinkStateDown}, "down"},
		{IfData{Flags: FlagUp | FlagRunning}, "unknown"},
		{IfData{Flags: FlagUp}, "down"},
	}
	for _, c := range cases {
		if got := c.d.OperState(); got != c.want {
			t.Errorf("%+v: got %q want %q", c.d, got, c.want)
		}
	}
}
