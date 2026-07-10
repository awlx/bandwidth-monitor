//go:build linux

package bandwidthtop

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	vnl "github.com/vishvananda/netlink"
)

type CaptureInterface struct {
	Interface *net.Interface
	LocalNets []*net.IPNet
	LocalIP   string
	Gateway   net.IP
}

func SelectInterface(name string) (*net.Interface, []*net.IPNet, string, error) {
	selected, err := SelectCaptureInterface(name)
	if err != nil {
		return nil, nil, "", err
	}
	return selected.Interface, selected.LocalNets, selected.LocalIP, nil
}

func SelectCaptureInterface(name string) (*CaptureInterface, error) {
	var iface *net.Interface
	var gateway net.IP
	var err error
	if name == "" {
		routes, routeErr := vnl.RouteList(nil, vnl.FAMILY_ALL)
		if routeErr != nil {
			return nil, fmt.Errorf("list default routes: %w", routeErr)
		}
		selected, routeErr := selectDefaultRoute(routes, net.InterfaceByIndex)
		if routeErr != nil {
			return nil, routeErr
		}
		iface = selected.Interface
		gateway = selected.Gateway
	} else {
		iface, err = net.InterfaceByName(name)
		if err != nil {
			return nil, fmt.Errorf("interface %q does not exist", name)
		}
	}
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("interface %q is down", iface.Name)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	nets, local := assignedLocalNetworks(addrs)
	if local == "" {
		return nil, fmt.Errorf("interface %q has no usable IP address", iface.Name)
	}

	return &CaptureInterface{
		Interface: iface, LocalNets: nets, LocalIP: local, Gateway: gateway,
	}, nil
}

func assignedLocalNetworks(addrs []net.Addr) ([]*net.IPNet, string) {
	type assignedNetwork struct {
		ip      net.IP
		network *net.IPNet
	}
	var assigned []assignedNetwork
	for _, addr := range addrs {
		ip, mask := assignedIPAndMask(addr)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		ones, bits := mask.Size()
		if bits == 0 || ones < 0 {
			continue
		}
		if ip.To4() != nil {
			if bits != 32 {
				continue
			}
			ip = ip.To4()
		} else {
			if bits != 128 {
				continue
			}
			ip = ip.To16()
		}
		if ip == nil {
			continue
		}
		assigned = append(assigned, assignedNetwork{
			ip:      ip,
			network: &net.IPNet{IP: ip.Mask(mask), Mask: append(net.IPMask(nil), mask...)},
		})
	}
	sort.Slice(assigned, func(i, j int) bool {
		left4, right4 := assigned[i].ip.To4(), assigned[j].ip.To4()
		if (left4 != nil) != (right4 != nil) {
			return left4 != nil
		}
		if comparison := bytes.Compare(assigned[i].ip, assigned[j].ip); comparison != 0 {
			return comparison < 0
		}
		leftPrefix, _ := assigned[i].network.Mask.Size()
		rightPrefix, _ := assigned[j].network.Mask.Size()
		return leftPrefix < rightPrefix
	})
	networks := make([]*net.IPNet, 0, len(assigned))
	local := ""
	for _, entry := range assigned {
		networks = append(networks, entry.network)
		if local == "" && !entry.ip.IsLinkLocalUnicast() {
			local = entry.ip.String()
		}
	}
	if local == "" && len(assigned) > 0 {
		local = assigned[0].ip.String()
	}
	return networks, local
}

func assignedIPAndMask(addr net.Addr) (net.IP, net.IPMask) {
	if network, ok := addr.(*net.IPNet); ok {
		return append(net.IP(nil), network.IP...), append(net.IPMask(nil), network.Mask...)
	}
	raw := addr.String()
	slash := strings.LastIndexByte(raw, '/')
	if slash < 0 {
		return nil, nil
	}
	host := raw[:slash]
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	ip := net.ParseIP(host)
	prefix, err := strconv.Atoi(raw[slash+1:])
	if err != nil || ip == nil {
		return nil, nil
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	if prefix < 0 || prefix > bits {
		return nil, nil
	}
	return ip, net.CIDRMask(prefix, bits)
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
		return nil, errorsNew("no active default-route interface found")
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

func errorsNew(message string) error { return fmt.Errorf("%s", message) }
