//go:build linux

package bandwidthtop

import (
	"fmt"
	"net"

	vnl "github.com/vishvananda/netlink"
)

func SelectInterface(name string) (*net.Interface, []*net.IPNet, string, error) {
	var iface *net.Interface
	var err error
	if name == "" {
		routes, routeErr := vnl.RouteList(nil, vnl.FAMILY_ALL)
		if routeErr != nil {
			return nil, nil, "", fmt.Errorf("list default routes: %w", routeErr)
		}
		iface, err = selectDefaultInterface(routes, net.InterfaceByIndex)
	} else {
		iface, err = net.InterfaceByName(name)
		if err != nil {
			return nil, nil, "", fmt.Errorf("interface %q does not exist", name)
		}
	}
	if err != nil {
		return nil, nil, "", err
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, nil, "", fmt.Errorf("interface %q is down", iface.Name)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, nil, "", err
	}
	var nets []*net.IPNet
	local := ""
	for _, addr := range addrs {
		ip, network, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		network.IP = ip
		nets = append(nets, network)
		if local == "" && !ip.IsLinkLocalUnicast() {
			local = ip.String()
		}
	}
	if local == "" {
		return nil, nil, "", fmt.Errorf("interface %q has no usable IP address", iface.Name)
	}
	return iface, nets, local, nil
}

func selectDefaultInterface(routes []vnl.Route, lookup func(int) (*net.Interface, error)) (*net.Interface, error) {
	var best *net.Interface
	bestMetric := int(^uint(0) >> 1)
	for _, route := range routes {
		if !isDefaultRoute(route) || route.LinkIndex <= 0 {
			continue
		}
		iface, err := lookup(route.LinkIndex)
		if err != nil || iface == nil || iface.Flags&net.FlagUp == 0 ||
			iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if best == nil || route.Priority < bestMetric ||
			(route.Priority == bestMetric && (iface.Name < best.Name ||
				(iface.Name == best.Name && iface.Index < best.Index))) {
			copy := *iface
			best = &copy
			bestMetric = route.Priority
		}
	}
	if best == nil {
		return nil, errorsNew("no active default-route interface found")
	}
	return best, nil
}

func isDefaultRoute(route vnl.Route) bool {
	if route.Dst == nil {
		return true
	}
	ones, _ := route.Dst.Mask.Size()
	return ones == 0
}

func errorsNew(message string) error { return fmt.Errorf("%s", message) }
