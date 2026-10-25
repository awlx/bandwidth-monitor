//go:build linux

package collector

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	vnl "github.com/vishvananda/netlink"
)

// linkSource enumerates interfaces and their counters via rtnetlink.
type linkSource struct {
	nlHandle      *vnl.Handle      // persistent netlink handle
	addrCache     map[int][]string // cached addresses by link index
	addrCacheTime time.Time        // last address refresh
}

func newLinkSource() *linkSource {
	// Create a persistent netlink handle to avoid per-poll socket creation.
	// Falls back to package-level functions if handle creation fails.
	nlh, err := vnl.NewHandle(vnl.FAMILY_ALL)
	if err != nil {
		log.Printf("collector: failed to create persistent netlink handle: %v (will use per-call sockets)", err)
	}
	return &linkSource{nlHandle: nlh, addrCache: make(map[int][]string)}
}

func (ls *linkSource) close() {
	if ls.nlHandle != nil {
		ls.nlHandle.Close()
	}
}

// list returns the current links keyed by name; ok is false when the link
// list could not be read at all.
func (ls *linkSource) list() (map[string]*linkInfo, bool) {
	var links []vnl.Link
	var err error
	if ls.nlHandle != nil {
		links, err = ls.nlHandle.LinkList()
	} else {
		links, err = vnl.LinkList()
	}
	if err != nil {
		log.Printf("collector: netlink LinkList: %v", err)
		return nil, false
	}

	// Refresh address cache every 10 seconds (addresses rarely change)
	if time.Since(ls.addrCacheTime) >= addrCacheTTL {
		var allAddrs []vnl.Addr
		if ls.nlHandle != nil {
			allAddrs, err = ls.nlHandle.AddrList(nil, vnl.FAMILY_ALL)
		} else {
			allAddrs, err = vnl.AddrList(nil, vnl.FAMILY_ALL)
		}
		if err != nil {
			log.Printf("collector: netlink AddrList: %v", err)
		} else {
			addrsByIndex := make(map[int][]string)
			for _, a := range allAddrs {
				addrsByIndex[a.LinkIndex] = append(addrsByIndex[a.LinkIndex], a.IPNet.String())
			}
			ls.addrCache = addrsByIndex
			ls.addrCacheTime = time.Now()
		}
	}

	infos := make(map[string]*linkInfo, len(links))
	for _, link := range links {
		attrs := link.Attrs()
		if attrs == nil {
			continue
		}
		name := attrs.Name

		li := &linkInfo{
			name:      name,
			operState: operStateStr(attrs.OperState),
			encapType: attrs.EncapType,
			addrs:     ls.addrCache[attrs.Index],
		}

		// Link speed (Mbps) — read from sysfs. Returns 0 for virtual interfaces.
		li.speed = readLinkSpeed(name)

		// Extract IFLA_INFO_KIND via link type name
		li.linkKind = link.Type()

		// Get stats from IFLA_STATS64 (vishvananda populates attrs.Statistics)
		if s := attrs.Statistics; s != nil {
			li.stats = &rawStat{
				rxBytes:   s.RxBytes,
				txBytes:   s.TxBytes,
				rxPackets: s.RxPackets,
				txPackets: s.TxPackets,
				rxErrors:  s.RxErrors,
				txErrors:  s.TxErrors,
				rxDropped: s.RxDropped,
				txDropped: s.TxDropped,
			}
		} else {
			li.stats = &rawStat{}
		}

		infos[name] = li
	}
	return infos, true
}

// readLinkSpeed reads the negotiated link speed in Mbps from sysfs.
// Returns 0 for virtual interfaces or if the file is unreadable.
func readLinkSpeed(name string) int {
	data, err := os.ReadFile("/sys/class/net/" + name + "/speed")
	if err != nil {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// hasDefaultRoute reports whether the named interface carries an IPv4 default route.
func hasDefaultRoute(ifaceName string) bool {
	routes, err := vnl.RouteList(nil, vnl.FAMILY_V4)
	if err != nil {
		return false
	}
	for _, r := range routes {
		// Default route: Dst is nil or 0.0.0.0/0
		if r.Dst != nil {
			ones, bits := r.Dst.Mask.Size()
			if ones != 0 || bits == 0 {
				continue
			}
		}
		link, err := vnl.LinkByIndex(r.LinkIndex)
		if err != nil {
			continue
		}
		if link.Attrs().Name == ifaceName {
			return true
		}
	}
	return false
}

// operStateStr converts a netlink OperState to a human-readable string.
func operStateStr(state vnl.LinkOperState) string {
	switch state {
	case vnl.OperUp:
		return "up"
	case vnl.OperDown:
		return "down"
	case vnl.OperLowerLayerDown:
		return "lowerlayerdown"
	case vnl.OperTesting:
		return "testing"
	case vnl.OperDormant:
		return "dormant"
	case vnl.OperNotPresent:
		return "notpresent"
	default:
		return "unknown"
	}
}
