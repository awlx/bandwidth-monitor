//go:build darwin

package bandwidthtop

import (
	"errors"
	"net"
	"syscall"
	"testing"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

func TestSelectDarwinDefaultRouteIsDeterministicAndExtractsGateway(t *testing.T) {
	messages := []route.Message{
		darwinTestRoute(syscall.AF_INET6, 4, net.ParseIP("2001:db8::1")),
		darwinTestRoute(syscall.AF_INET, 3, net.ParseIP("192.0.2.20")),
		darwinTestRoute(syscall.AF_INET, 2, net.ParseIP("192.0.2.10")),
	}
	selected, err := selectDarwinDefaultRoute(messages, darwinInterfaceLookup(map[int]*net.Interface{
		2: {Index: 2, Name: "test0", Flags: net.FlagUp},
		3: {Index: 3, Name: "test1", Flags: net.FlagUp},
		4: {Index: 4, Name: "test6", Flags: net.FlagUp},
	}), 0)
	if err != nil || selected.iface.Name != "test0" ||
		!selected.gateway.Equal(net.ParseIP("192.0.2.10")) {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
}

func TestSelectDarwinDefaultRouteHonorsExplicitInterface(t *testing.T) {
	messages := []route.Message{
		darwinTestRoute(syscall.AF_INET, 2, net.ParseIP("192.0.2.10")),
		darwinTestRoute(syscall.AF_INET, 3, net.ParseIP("192.0.2.20")),
	}
	selected, err := selectDarwinDefaultRoute(messages, darwinInterfaceLookup(map[int]*net.Interface{
		2: {Index: 2, Name: "test0", Flags: net.FlagUp},
		3: {Index: 3, Name: "test1", Flags: net.FlagUp},
	}), 3)
	if err != nil || selected.iface.Index != 3 ||
		!selected.gateway.Equal(net.ParseIP("192.0.2.20")) {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
}

func TestSelectDarwinDefaultRouteSkipsScopedAndGatewaylessDefaults(t *testing.T) {
	scoped := darwinTestRoute(syscall.AF_INET, 2, net.ParseIP("192.0.2.10"))
	scoped.(*route.RouteMessage).Flags |= unix.RTF_IFSCOPE
	gatewayless := darwinTestRoute(syscall.AF_INET, 3, net.ParseIP("192.0.2.20"))
	gatewayless.(*route.RouteMessage).Flags &^= unix.RTF_GATEWAY
	primary := darwinTestRoute(syscall.AF_INET, 4, net.ParseIP("192.0.2.30"))
	selected, err := selectDarwinDefaultRoute(
		[]route.Message{scoped, gatewayless, primary},
		darwinInterfaceLookup(map[int]*net.Interface{
			2: {Index: 2, Name: "test0", Flags: net.FlagUp},
			3: {Index: 3, Name: "test1", Flags: net.FlagUp},
			4: {Index: 4, Name: "test2", Flags: net.FlagUp},
		}),
		0,
	)
	if err != nil || selected.iface.Index != 4 ||
		!selected.gateway.Equal(net.ParseIP("192.0.2.30")) {
		t.Fatalf("selected=%+v err=%v", selected, err)
	}
}

func TestSelectDarwinDefaultRouteRejectsInactiveAndMalformedRoutes(t *testing.T) {
	specific := darwinTestRoute(syscall.AF_INET, 2, net.ParseIP("192.0.2.1"))
	specific.(*route.RouteMessage).Addrs[syscall.RTAX_DST] = &route.Inet4Addr{IP: [4]byte{192, 0, 2, 0}}
	messages := []route.Message{
		specific,
		darwinTestRoute(syscall.AF_INET, 1, net.ParseIP("192.0.2.1")),
		darwinTestRoute(syscall.AF_INET, 2, net.ParseIP("192.0.2.2")),
	}
	_, err := selectDarwinDefaultRoute(messages, darwinInterfaceLookup(map[int]*net.Interface{
		1: {Index: 1, Name: "loop", Flags: net.FlagUp | net.FlagLoopback},
		2: {Index: 2, Name: "down"},
	}), 0)
	if !errors.Is(err, errNoDarwinDefaultRoute) {
		t.Fatalf("got %v", err)
	}
}

func darwinTestRoute(family, index int, gateway net.IP) route.Message {
	addrs := make([]route.Addr, syscall.RTAX_MAX)
	if family == syscall.AF_INET {
		addrs[syscall.RTAX_DST] = &route.Inet4Addr{}
		var value [4]byte
		copy(value[:], gateway.To4())
		addrs[syscall.RTAX_GATEWAY] = &route.Inet4Addr{IP: value}
		addrs[syscall.RTAX_NETMASK] = &route.Inet4Addr{}
	} else {
		addrs[syscall.RTAX_DST] = &route.Inet6Addr{}
		var value [16]byte
		copy(value[:], gateway.To16())
		addrs[syscall.RTAX_GATEWAY] = &route.Inet6Addr{IP: value}
		addrs[syscall.RTAX_NETMASK] = &route.Inet6Addr{}
	}
	return &route.RouteMessage{
		Flags: unix.RTF_UP | unix.RTF_GATEWAY,
		Index: index,
		Addrs: addrs,
	}
}

func darwinInterfaceLookup(
	interfaces map[int]*net.Interface,
) func(int) (*net.Interface, error) {
	return func(index int) (*net.Interface, error) {
		if iface := interfaces[index]; iface != nil {
			return iface, nil
		}
		return nil, errors.New("missing interface")
	}
}
