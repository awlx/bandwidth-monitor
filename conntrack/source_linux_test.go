//go:build linux

package conntrack

import (
	"net/netip"
	"testing"

	ct "github.com/ti-mo/conntrack"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	return netip.MustParseAddr(s)
}

// With hardware flow offloading enabled, the kernel omits CTA_PROTOINFO for
// offloaded flows, so a TCP entry arrives with a nil ProtoInfo.TCP. This used
// to panic (nil pointer dereference) in convertFlow; see issue #160.
func TestConvertFlowNilProtoInfo(t *testing.T) {
	f := &ct.Flow{
		TupleOrig: ct.Tuple{
			IP: ct.IPTuple{
				SourceAddress:      mustAddr(t, "192.168.1.100"),
				DestinationAddress: mustAddr(t, "93.184.216.34"),
			},
			Proto: ct.ProtoTuple{Protocol: 6, DestinationPort: 443},
		},
		TupleReply: ct.Tuple{
			IP: ct.IPTuple{
				SourceAddress:      mustAddr(t, "93.184.216.34"),
				DestinationAddress: mustAddr(t, "192.168.1.100"),
			},
			Proto: ct.ProtoTuple{Protocol: 6, SourcePort: 443},
		},
		Timeout: 432000,
	}

	e := convertFlow(f)

	if e.Protocol != "TCP" {
		t.Errorf("Protocol = %q, want TCP", e.Protocol)
	}
	if e.State != "" {
		t.Errorf("State = %q, want empty for offloaded flow without protoinfo", e.State)
	}
	if e.OrigSrc != "192.168.1.100" || e.OrigDst != "93.184.216.34" {
		t.Errorf("orig tuple = %q -> %q, want 192.168.1.100 -> 93.184.216.34", e.OrigSrc, e.OrigDst)
	}
}

// Normal TCP flow with protoinfo present still maps the state.
func TestConvertFlowTCPState(t *testing.T) {
	f := &ct.Flow{
		TupleOrig: ct.Tuple{
			IP: ct.IPTuple{
				SourceAddress:      mustAddr(t, "192.168.1.100"),
				DestinationAddress: mustAddr(t, "93.184.216.34"),
			},
			Proto: ct.ProtoTuple{Protocol: 6, SourcePort: 50000, DestinationPort: 443},
		},
		ProtoInfo: ct.ProtoInfo{TCP: &ct.ProtoInfoTCP{State: 3}},
	}

	e := convertFlow(f)

	if e.State != "ESTABLISHED" {
		t.Errorf("State = %q, want ESTABLISHED", e.State)
	}
}
