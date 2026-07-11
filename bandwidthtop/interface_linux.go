//go:build linux

package bandwidthtop

import (
	"bytes"
	"fmt"
	"net"

	vnl "github.com/vishvananda/netlink"
)

func selectCaptureTarget(name string) (*net.Interface, net.IP, error) {
	if name != "" {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			return nil, nil, fmt.Errorf("interface %q does not exist", name)
		}
		return iface, nil, nil
	}
	routes, err := vnl.RouteList(nil, vnl.FAMILY_ALL)
	if err != nil {
		return nil, nil, fmt.Errorf("list default routes: %w", err)
	}
	selected, err := selectDefaultRoute(routes, net.InterfaceByIndex)
	if err != nil {
		return nil, nil, err
	}
	return selected.Interface, selected.Gateway, nil
}

func selectDefaultInterface(routes []vnl.Route, lookup func(int) (*net.Interface, error)) (*net.Interface, error) {
	selected, err := selectDefaultRoute(routes, lookup)
	if err != nil {
		return nil, err
	}
	return selected.Interface, nil
}

type defaultRoute struct {
	Interface *net.Interface
	Gateway   net.IP
}

func selectDefaultRoute(routes []vnl.Route, lookup func(int) (*net.Interface, error)) (*defaultRoute, error) {
	var best *defaultRoute
	bestMetric := int(^uint(0) >> 1)
	for _, route := range routes {
		if !isDefaultRoute(route) {
			continue
		}
		type routeTarget struct {
			index   int
			gateway net.IP
		}
		targets := make([]routeTarget, 0, 1+len(route.MultiPath))
		if route.LinkIndex > 0 && len(route.MultiPath) == 0 {
			targets = append(targets, routeTarget{index: route.LinkIndex, gateway: route.Gw})
		}
		for _, nexthop := range route.MultiPath {
			if nexthop != nil && nexthop.LinkIndex > 0 {
				targets = append(targets, routeTarget{index: nexthop.LinkIndex, gateway: nexthop.Gw})
			}
		}
		for _, target := range targets {
			iface, err := lookup(target.index)
			if err != nil || iface == nil || iface.Flags&net.FlagUp == 0 ||
				iface.Flags&net.FlagLoopback != 0 {
				continue
			}
			// Nexthop Hops is a weight, not route preference, so it does not
			// participate in selection. Metric, name, index, then gateway bytes
			// are stable and retain the chosen nexthop's gateway.
			if best == nil || route.Priority < bestMetric ||
				(route.Priority == bestMetric && routeTargetLess(iface, target.gateway, best)) {
				copy := *iface
				best = &defaultRoute{
					Interface: &copy,
					Gateway:   append(net.IP(nil), target.gateway...),
				}
				bestMetric = route.Priority
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("no active default-route interface found")
	}
	return best, nil
}

func routeTargetLess(iface *net.Interface, gateway net.IP, best *defaultRoute) bool {
	if iface.Name != best.Interface.Name {
		return iface.Name < best.Interface.Name
	}
	if iface.Index != best.Interface.Index {
		return iface.Index < best.Interface.Index
	}
	return bytes.Compare(gateway, best.Gateway) < 0
}

func isDefaultRoute(route vnl.Route) bool {
	if route.Dst == nil {
		return true
	}
	ones, _ := route.Dst.Mask.Size()
	return ones == 0
}
