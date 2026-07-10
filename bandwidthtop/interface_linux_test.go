//go:build linux

package bandwidthtop

import (
	"errors"
	"net"
	"testing"

	vnl "github.com/vishvananda/netlink"
)

type testAddr string

func (a testAddr) Network() string { return "test" }
func (a testAddr) String() string  { return string(a) }

func TestAssignedLocalNetworksPreserveInterfaceTopology(t *testing.T) {
	addrs := []net.Addr{
		&net.IPNet{IP: net.ParseIP("192.168.1.7"), Mask: net.CIDRMask(24, 32)},
		&net.IPNet{IP: net.ParseIP("203.0.113.18"), Mask: net.CIDRMask(27, 32)},
		testAddr("2001:db8:1::7/64"),
		testAddr("fe80::1%eth0/64"),
		&net.IPNet{IP: net.ParseIP("198.51.100.9"), Mask: net.CIDRMask(32, 32)},
		&net.IPNet{IP: net.ParseIP("2001:db8:2::9"), Mask: net.CIDRMask(128, 128)},
		&net.IPNet{IP: net.ParseIP("192.0.2.1"), Mask: net.IPMask{0xff, 0x00}},
		&net.IPNet{IP: net.ParseIP("2001:db8:3::1"), Mask: net.CIDRMask(24, 32)},
		testAddr("invalid"),
	}
	networks, local := assignedLocalNetworks(addrs)
	if local != "192.168.1.7" || len(networks) != 6 {
		t.Fatalf("local=%q networks=%v", local, networks)
	}
	cases := []struct {
		ip    string
		local bool
	}{
		{"192.168.1.20", true},
		{"192.168.2.20", false},
		{"10.0.0.1", false},
		{"203.0.113.20", true},
		{"203.0.113.40", false},
		{"2001:db8:1::20", true},
		{"fd00::1", false},
		{"fe80::20", true},
		{"198.51.100.9", true},
		{"198.51.100.10", false},
		{"2001:db8:2::9", true},
		{"2001:db8:2::10", false},
	}
	for _, test := range cases {
		found := false
		for _, network := range networks {
			found = found || network.Contains(net.ParseIP(test.ip))
		}
		if found != test.local {
			t.Fatalf("%s local=%v, want %v (networks %v)", test.ip, found, test.local, networks)
		}
	}
}

func TestAssignedLocalNetworksAcceptLinkLocalOnlyInterface(t *testing.T) {
	networks, local := assignedLocalNetworks([]net.Addr{testAddr("fe80::1%eth0/64")})
	if local != "fe80::1" || len(networks) != 1 || !networks[0].Contains(net.ParseIP("fe80::2")) {
		t.Fatalf("local=%q networks=%v", local, networks)
	}
}

func TestAssignedLocalNetworkSelectionIsOrderIndependent(t *testing.T) {
	first := []net.Addr{
		testAddr("2001:db8:2::2/64"),
		testAddr("192.0.2.20/24"),
		testAddr("192.0.2.10/24"),
	}
	second := []net.Addr{first[2], first[0], first[1]}
	firstNetworks, firstLocal := assignedLocalNetworks(first)
	secondNetworks, secondLocal := assignedLocalNetworks(second)
	if firstLocal != "192.0.2.10" || secondLocal != firstLocal ||
		len(firstNetworks) != len(secondNetworks) {
		t.Fatalf("first=%q %v second=%q %v", firstLocal, firstNetworks, secondLocal, secondNetworks)
	}
	for i := range firstNetworks {
		if firstNetworks[i].String() != secondNetworks[i].String() {
			t.Fatalf("network order differs: %v vs %v", firstNetworks, secondNetworks)
		}
	}
}

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
