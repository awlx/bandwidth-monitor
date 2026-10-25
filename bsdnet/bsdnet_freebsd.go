//go:build freebsd

package bsdnet

import (
	"net"

	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

// Interfaces returns every interface with its current kernel counters.
func Interfaces() ([]IfData, error) {
	rib, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBTypeInterface, 0)
	if err != nil {
		return nil, err
	}
	ifs := ParseIfList(rib)
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	names := make(map[int]string, len(ifaces))
	for _, ifc := range ifaces {
		names[ifc.Index] = ifc.Name
	}
	out := ifs[:0]
	for _, d := range ifs {
		if name, ok := names[d.Index]; ok {
			d.Name = name
			out = append(out, d)
		}
	}
	return out, nil
}

// DefaultRoutes returns the IPv4 and IPv6 default routes in the main FIB.
func DefaultRoutes() ([]DefaultRoute, error) {
	rib, err := route.FetchRIB(unix.AF_UNSPEC, route.RIBTypeRoute, 0)
	if err != nil {
		return nil, err
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return nil, err
	}
	var out []DefaultRoute
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || rm.Flags&unix.RTF_UP == 0 || rm.Flags&unix.RTF_HOST != 0 {
			continue
		}
		if dr, ok := defaultRouteFromAddrs(rm.Index, rm.Addrs); ok {
			out = append(out, dr)
		}
	}
	return out, nil
}

func defaultRouteFromAddrs(index int, addrs []route.Addr) (DefaultRoute, bool) {
	if len(addrs) <= unix.RTAX_GATEWAY {
		return DefaultRoute{}, false
	}
	dst := addrIP(addrs[unix.RTAX_DST])
	if dst == nil || !dst.IsUnspecified() {
		return DefaultRoute{}, false
	}
	if len(addrs) > unix.RTAX_NETMASK {
		if mask := addrIP(addrs[unix.RTAX_NETMASK]); mask != nil && !mask.IsUnspecified() {
			return DefaultRoute{}, false
		}
	}
	dr := DefaultRoute{Index: index, IPv6: dst.To4() == nil}
	switch gw := addrs[unix.RTAX_GATEWAY].(type) {
	case *route.LinkAddr:
		if gw.Index > 0 && dr.Index == 0 {
			dr.Index = gw.Index
		}
	default:
		dr.Gateway = addrIP(gw)
	}
	return dr, dr.Index > 0
}

func addrIP(a route.Addr) net.IP {
	switch v := a.(type) {
	case *route.Inet4Addr:
		return net.IP(v.IP[:]).To16()
	case *route.Inet6Addr:
		return net.IP(v.IP[:])
	}
	return nil
}
