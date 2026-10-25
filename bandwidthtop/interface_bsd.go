//go:build darwin || freebsd

package bandwidthtop

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"syscall"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

var errNoBSDDefaultRoute = errors.New("no active default-route interface found")

type bsdRouteTarget struct {
	iface   *net.Interface
	gateway net.IP
	family  int
}

func selectCaptureTarget(name string) (*net.Interface, net.IP, error) {
	if name != "" {
		iface, lookupErr := net.InterfaceByName(name)
		if lookupErr != nil {
			return nil, nil, fmt.Errorf("interface %q does not exist", name)
		}
		messages, err := bsdRouteMessages()
		if err != nil {
			return nil, nil, err
		}
		selected, routeErr := selectBSDDefaultRoute(messages, net.InterfaceByIndex, iface.Index)
		if routeErr != nil && !errors.Is(routeErr, errNoBSDDefaultRoute) {
			return nil, nil, routeErr
		}
		if routeErr == nil {
			return iface, selected.gateway, nil
		}
		return iface, nil, nil
	}

	messages, err := bsdRouteMessages()
	if err != nil {
		return nil, nil, err
	}
	selected, err := selectBSDDefaultRoute(messages, net.InterfaceByIndex, 0)
	if err != nil {
		return nil, nil, err
	}
	return selected.iface, selected.gateway, nil
}

func bsdRouteMessages() ([]route.Message, error) {
	rib, err := route.FetchRIB(syscall.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, fmt.Errorf("read BSD routing table: %w", err)
	}
	messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, fmt.Errorf("parse BSD routing table: %w", err)
	}
	return messages, nil
}

func selectBSDDefaultRoute(
	messages []route.Message,
	lookup func(int) (*net.Interface, error),
	preferredIndex int,
) (*bsdRouteTarget, error) {
	var best *bsdRouteTarget
	for _, message := range messages {
		routeMessage, ok := message.(*route.RouteMessage)
		if !ok || routeMessage.Flags&unix.RTF_UP == 0 ||
			routeMessage.Flags&unix.RTF_GATEWAY == 0 ||
			routeMessage.Flags&(unix.RTF_REJECT|unix.RTF_BLACKHOLE|unix.RTF_HOST) != 0 ||
			preferredIndex == 0 && routeMessage.Flags&routeFlagIfScope != 0 {
			continue
		}
		family, ok := bsdDefaultRouteFamily(routeMessage.Addrs)
		if !ok {
			continue
		}
		index := routeMessage.Index
		if index == 0 {
			index = bsdRouteInterfaceIndex(routeMessage.Addrs)
		}
		if index <= 0 || preferredIndex > 0 && index != preferredIndex {
			continue
		}
		iface, err := lookup(index)
		if err != nil || iface == nil || iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		candidate := &bsdRouteTarget{
			iface:   cloneInterface(iface),
			gateway: bsdRouteGateway(routeMessage.Addrs),
			family:  family,
		}
		if best == nil || bsdRouteLess(candidate, best) {
			best = candidate
		}
	}
	if best == nil {
		return nil, errNoBSDDefaultRoute
	}
	return best, nil
}

func bsdDefaultRouteFamily(addrs []route.Addr) (int, bool) {
	destination := bsdRouteAddr(addrs, syscall.RTAX_DST)
	mask := bsdRouteAddr(addrs, syscall.RTAX_NETMASK)
	switch address := destination.(type) {
	case *route.Inet4Addr:
		if address.IP != [4]byte{} || !bsdRouteMaskIsZero(mask) {
			return 0, false
		}
		return syscall.AF_INET, true
	case *route.Inet6Addr:
		if address.IP != [16]byte{} || !bsdRouteMaskIsZero(mask) {
			return 0, false
		}
		return syscall.AF_INET6, true
	default:
		return 0, false
	}
}

func bsdRouteMaskIsZero(address route.Addr) bool {
	switch mask := address.(type) {
	case nil:
		return true
	case *route.Inet4Addr:
		return mask.IP == [4]byte{}
	case *route.Inet6Addr:
		return mask.IP == [16]byte{}
	case *route.DefaultAddr:
		for _, value := range mask.Raw {
			if value != 0 {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func bsdRouteInterfaceIndex(addrs []route.Addr) int {
	if address, ok := bsdRouteAddr(addrs, syscall.RTAX_IFP).(*route.LinkAddr); ok {
		return address.Index
	}
	return 0
}

func bsdRouteGateway(addrs []route.Addr) net.IP {
	switch address := bsdRouteAddr(addrs, syscall.RTAX_GATEWAY).(type) {
	case *route.Inet4Addr:
		return append(net.IP(nil), address.IP[:]...)
	case *route.Inet6Addr:
		return append(net.IP(nil), address.IP[:]...)
	default:
		return nil
	}
}

func bsdRouteAddr(addrs []route.Addr, index int) route.Addr {
	if index < 0 || index >= len(addrs) {
		return nil
	}
	return addrs[index]
}

func bsdRouteLess(left, right *bsdRouteTarget) bool {
	if left.family != right.family {
		return left.family == syscall.AF_INET
	}
	if left.iface.Name != right.iface.Name {
		return left.iface.Name < right.iface.Name
	}
	if left.iface.Index != right.iface.Index {
		return left.iface.Index < right.iface.Index
	}
	return bytes.Compare(left.gateway, right.gateway) < 0
}

func cloneInterface(iface *net.Interface) *net.Interface {
	copy := *iface
	copy.HardwareAddr = append(net.HardwareAddr(nil), iface.HardwareAddr...)
	return &copy
}
