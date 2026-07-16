//go:build linux

package talkers

import (
	"net"

	vnl "github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func defaultRouteInterfaceIndexes() (map[int]bool, error) {
	routes, err := vnl.RouteListFiltered(
		vnl.FAMILY_ALL,
		&vnl.Route{Table: unix.RT_TABLE_UNSPEC},
		vnl.RT_FILTER_TABLE,
	)
	if err != nil {
		return nil, err
	}
	indexes := make(map[int]bool)
	for _, route := range routes {
		if route.LinkIndex > 0 && isDefaultRoute(route.Dst) {
			indexes[route.LinkIndex] = true
		}
	}
	return indexes, nil
}

func isDefaultRoute(dst *net.IPNet) bool {
	if dst == nil {
		return true
	}
	ones, bits := dst.Mask.Size()
	return ones == 0 && bits != 0
}
