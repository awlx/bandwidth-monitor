//go:build freebsd

package collector

import (
	"log"
	"net"
	"time"

	"bandwidth-monitor/bsdnet"
)

// linkSource enumerates interfaces and their counters via the FreeBSD
// routing socket (sysctl NET_RT_IFLIST).
type linkSource struct {
	addrCache     map[int][]string
	addrCacheTime time.Time
}

func newLinkSource() *linkSource {
	return &linkSource{addrCache: make(map[int][]string)}
}

func (ls *linkSource) close() {}

func (ls *linkSource) list() (map[string]*linkInfo, bool) {
	ifs, err := bsdnet.Interfaces()
	if err != nil {
		log.Printf("collector: interface list: %v", err)
		return nil, false
	}
	if time.Since(ls.addrCacheTime) >= addrCacheTTL {
		ls.refreshAddrs()
	}

	infos := make(map[string]*linkInfo, len(ifs))
	for _, d := range ifs {
		kind, encap := bsdLinkKind(d.Name, d.Type)
		infos[d.Name] = &linkInfo{
			name:      d.Name,
			operState: d.OperState(),
			encapType: encap,
			linkKind:  kind,
			speed:     int(d.Baudrate / 1_000_000),
			addrs:     ls.addrCache[d.Index],
			stats: &rawStat{
				rxBytes:   d.RxBytes,
				txBytes:   d.TxBytes,
				rxPackets: d.RxPackets,
				txPackets: d.TxPackets,
				rxErrors:  d.RxErrors,
				txErrors:  d.TxErrors,
				rxDropped: d.RxDropped,
				txDropped: d.TxDropped,
			},
		}
	}
	return infos, true
}

func (ls *linkSource) refreshAddrs() {
	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("collector: interface addresses: %v", err)
		return
	}
	addrsByIndex := make(map[int][]string, len(ifaces))
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				addrsByIndex[ifc.Index] = append(addrsByIndex[ifc.Index], ipn.String())
			}
		}
	}
	ls.addrCache = addrsByIndex
	ls.addrCacheTime = time.Now()
}

// hasDefaultRoute reports whether the named interface carries an IPv4 default route.
func hasDefaultRoute(ifaceName string) bool {
	routes, err := bsdnet.DefaultRoutes()
	if err != nil {
		return false
	}
	for _, r := range routes {
		if r.IPv6 {
			continue
		}
		if ifc, err := net.InterfaceByIndex(r.Index); err == nil && ifc.Name == ifaceName {
			return true
		}
	}
	return false
}
