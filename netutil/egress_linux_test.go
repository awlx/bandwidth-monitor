//go:build linux

package netutil

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestSameRoute(t *testing.T) {
	gw := net.ParseIP("192.0.2.1")
	a := netlink.Route{LinkIndex: 2, Table: 254, Gw: gw}
	if !sameRoute(a, netlink.Route{LinkIndex: 2, Table: 254, Gw: net.ParseIP("192.0.2.1")}) {
		t.Error("identical routes differ")
	}
	for _, b := range []netlink.Route{
		{LinkIndex: 3, Table: 254, Gw: gw},
		{LinkIndex: 2, Table: 100, Gw: gw},
		{LinkIndex: 2, Table: 254, Gw: net.ParseIP("192.0.2.9")},
	} {
		if sameRoute(a, b) {
			t.Errorf("%+v reported same as %+v", b, a)
		}
	}
}
