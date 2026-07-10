//go:build linux

package bandwidthtop

import (
	"errors"
	"net"
	"testing"

	vnl "github.com/vishvananda/netlink"
)

func TestDefaultInterfaceChoosesLowestMetricAcrossFamilies(t *testing.T) {
	_, ipv6Default, _ := net.ParseCIDR("::/0")
	routes := []vnl.Route{
		{LinkIndex: 2, Priority: 200},
		{LinkIndex: 3, Priority: 10, Dst: ipv6Default},
	}
	got, err := selectDefaultInterface(routes, interfaceLookup(map[int]*net.Interface{
		2: {Index: 2, Name: "eth1", Flags: net.FlagUp},
		3: {Index: 3, Name: "eth0", Flags: net.FlagUp},
	}))
	if err != nil || got.Name != "eth0" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDefaultInterfaceSupportsIPv6Only(t *testing.T) {
	_, ipv6Default, _ := net.ParseCIDR("::/0")
	got, err := selectDefaultInterface([]vnl.Route{{
		LinkIndex: 7, Priority: 5, Dst: ipv6Default,
	}}, interfaceLookup(map[int]*net.Interface{
		7: {Index: 7, Name: "wan6", Flags: net.FlagUp},
	}))
	if err != nil || got.Name != "wan6" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDefaultInterfaceTieIsDeterministic(t *testing.T) {
	routes := []vnl.Route{
		{LinkIndex: 4, Priority: 10},
		{LinkIndex: 2, Priority: 10},
	}
	got, err := selectDefaultInterface(routes, interfaceLookup(map[int]*net.Interface{
		4: {Index: 4, Name: "eth1", Flags: net.FlagUp},
		2: {Index: 2, Name: "eth0", Flags: net.FlagUp},
	}))
	if err != nil || got.Name != "eth0" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDefaultInterfaceSkipsDownMissingLoopbackAndNonDefault(t *testing.T) {
	_, nonDefault, _ := net.ParseCIDR("192.0.2.0/24")
	routes := []vnl.Route{
		{LinkIndex: 1, Priority: 1},
		{LinkIndex: 2, Priority: 2},
		{LinkIndex: 3, Priority: 3},
		{LinkIndex: 4, Priority: 4, Dst: nonDefault},
		{LinkIndex: 5, Priority: 5},
	}
	got, err := selectDefaultInterface(routes, interfaceLookup(map[int]*net.Interface{
		1: {Index: 1, Name: "lo", Flags: net.FlagUp | net.FlagLoopback},
		2: {Index: 2, Name: "down0", Flags: 0},
		4: {Index: 4, Name: "specific0", Flags: net.FlagUp},
		5: {Index: 5, Name: "eth0", Flags: net.FlagUp},
	}))
	if err != nil || got.Name != "eth0" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDefaultInterfaceSupportsIPv4Multipath(t *testing.T) {
	routes := []vnl.Route{{
		Priority: 20,
		MultiPath: []*vnl.NexthopInfo{
			{LinkIndex: 2, Hops: 0, Gw: net.ParseIP("192.0.2.2")},
			{LinkIndex: 3, Hops: 8, Gw: net.ParseIP("192.0.2.3")},
			{LinkIndex: 4, Hops: 1, Gw: net.ParseIP("192.0.2.4")},
		},
	}}
	got, err := selectDefaultRoute(routes, interfaceLookup(map[int]*net.Interface{
		2: {Index: 2, Name: "down0"},
		3: {Index: 3, Name: "wan1", Flags: net.FlagUp},
		4: {Index: 4, Name: "wan0", Flags: net.FlagUp},
	}))
	if err != nil || got.Interface.Name != "wan0" || !got.Gateway.Equal(net.ParseIP("192.0.2.4")) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDefaultInterfaceSupportsIPv6MultipathAndRouteMetric(t *testing.T) {
	_, ipv6Default, _ := net.ParseCIDR("::/0")
	routes := []vnl.Route{
		{
			Priority: 50,
			Dst:      ipv6Default,
			MultiPath: []*vnl.NexthopInfo{
				nil,
				{LinkIndex: 8, Hops: 0, Gw: net.ParseIP("2001:db8::8")},
			},
		},
		{
			Priority: 10,
			Dst:      ipv6Default,
			MultiPath: []*vnl.NexthopInfo{
				{LinkIndex: 9, Hops: 20, Gw: net.ParseIP("2001:db8::9")},
			},
		},
	}
	got, err := selectDefaultRoute(routes, interfaceLookup(map[int]*net.Interface{
		8: {Index: 8, Name: "wan0", Flags: net.FlagUp},
		9: {Index: 9, Name: "wan6", Flags: net.FlagUp},
	}))
	if err != nil || got.Interface.Name != "wan6" || !got.Gateway.Equal(net.ParseIP("2001:db8::9")) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDefaultRouteGatewayTieIsDeterministic(t *testing.T) {
	routes := []vnl.Route{{
		Priority: 10,
		MultiPath: []*vnl.NexthopInfo{
			{LinkIndex: 2, Gw: net.ParseIP("192.0.2.20")},
			{LinkIndex: 2, Gw: net.ParseIP("192.0.2.10")},
		},
	}}
	got, err := selectDefaultRoute(routes, interfaceLookup(map[int]*net.Interface{
		2: {Index: 2, Name: "wan0", Flags: net.FlagUp},
	}))
	if err != nil || !got.Gateway.Equal(net.ParseIP("192.0.2.10")) {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func interfaceLookup(interfaces map[int]*net.Interface) func(int) (*net.Interface, error) {
	return func(index int) (*net.Interface, error) {
		if iface := interfaces[index]; iface != nil {
			return iface, nil
		}
		return nil, errors.New("missing interface")
	}
}
