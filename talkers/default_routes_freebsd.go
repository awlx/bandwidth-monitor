//go:build freebsd

package talkers

import "bandwidth-monitor/bsdnet"

func defaultRouteInterfaceIndexes() (map[int]bool, error) {
	routes, err := bsdnet.DefaultRoutes()
	if err != nil {
		return nil, err
	}
	indexes := make(map[int]bool)
	for _, r := range routes {
		indexes[r.Index] = true
	}
	return indexes, nil
}
