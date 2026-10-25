//go:build linux

package topology

import (
	"net"

	vnl "github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"
)

// listNeighbors returns the kernel ARP (IPv4) or NDP (IPv6) table via netlink.
func listNeighbors(ipv6 bool) ([]neighbor, error) {
	family := vnl.FAMILY_V4
	if ipv6 {
		family = vnl.FAMILY_V6
	}
	neighs, err := vnl.NeighList(0, family)
	if err != nil {
		return nil, err
	}
	out := make([]neighbor, 0, len(neighs))
	for _, n := range neighs {
		out = append(out, neighbor{
			IP:        n.IP,
			MAC:       n.HardwareAddr,
			LinkIndex: n.LinkIndex,
			State:     neighStateStr(n.State),
			Valid:     neighValid(n),
		})
	}
	return out, nil
}

// defaultRoutePeers returns IPv4 default routes with the peer addresses of
// their outgoing interface (set on point-to-point links such as PPP).
func defaultRoutePeers() ([]routePeer, error) {
	routes, err := vnl.RouteList(nil, vnl.FAMILY_V4)
	if err != nil {
		return nil, err
	}
	ifaceNames := ifaceIndexNames()

	var out []routePeer
	for _, r := range routes {
		// Point-to-point default route: either Dst==nil or Dst==0.0.0.0/0
		if r.Dst != nil {
			ones, bits := r.Dst.Mask.Size()
			if ones != 0 || bits == 0 {
				continue
			}
		}
		rp := routePeer{Iface: ifaceNames[r.LinkIndex]}
		// For PPP: get the peer/remote IP from the interface addresses
		link, err := vnl.LinkByIndex(r.LinkIndex)
		if err != nil {
			continue
		}
		addrs, err := vnl.AddrList(link, vnl.FAMILY_V4)
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			if addr.Peer != nil && addr.Peer.IP != nil {
				rp.Peers = append(rp.Peers, addr.Peer.IP)
			}
		}
		out = append(out, rp)
	}
	return out, nil
}

// wireGuardDevices reads WireGuard peers via the kernel's generic netlink API.
func wireGuardDevices() ([]wgDevice, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	defer client.Close()

	devices, err := client.Devices()
	if err != nil {
		return nil, err
	}
	out := make([]wgDevice, 0, len(devices))
	for _, dev := range devices {
		d := wgDevice{Name: dev.Name}
		for _, p := range dev.Peers {
			var ep net.IP
			if p.Endpoint != nil {
				ep = p.Endpoint.IP
			}
			d.Peers = append(d.Peers, wgPeer{
				Endpoint:      ep,
				AllowedIPs:    p.AllowedIPs,
				ReceiveBytes:  p.ReceiveBytes,
				TransmitBytes: p.TransmitBytes,
			})
		}
		out = append(out, d)
	}
	return out, nil
}

// defaultGatewayIP returns the IPv4 default gateway using netlink routes.
func defaultGatewayIP() string {
	routes, err := vnl.RouteList(nil, vnl.FAMILY_V4)
	if err != nil {
		return ""
	}
	for _, r := range routes {
		if r.Dst == nil && r.Gw != nil {
			return r.Gw.String()
		}
	}
	for _, r := range routes {
		if r.Dst != nil && r.Gw != nil {
			ones, bits := r.Dst.Mask.Size()
			if ones == 0 && bits > 0 {
				return r.Gw.String()
			}
		}
	}
	return ""
}

// ifaceIndexNames builds a map from link index to interface name.
func ifaceIndexNames() map[int]string {
	links, err := vnl.LinkList()
	if err != nil {
		return nil
	}
	m := make(map[int]string, len(links))
	for _, l := range links {
		if a := l.Attrs(); a != nil {
			m[a.Index] = a.Name
		}
	}
	return m
}

// buildVLANParentMap returns a map from VLAN interface name to its parent
// physical interface name (e.g. "vlan3000" -> "enp3s0").
func buildVLANParentMap() map[string]string {
	links, err := vnl.LinkList()
	if err != nil {
		return nil
	}
	idxToName := make(map[int]string, len(links))
	for _, l := range links {
		if a := l.Attrs(); a != nil {
			idxToName[a.Index] = a.Name
		}
	}
	m := make(map[string]string)
	for _, l := range links {
		a := l.Attrs()
		if a == nil {
			continue
		}
		if a.ParentIndex > 0 {
			if parent, ok := idxToName[a.ParentIndex]; ok {
				m[a.Name] = parent
			}
		}
	}
	return m
}

// neighValid returns true if a neighbor entry has a resolved hardware address.
func neighValid(n vnl.Neigh) bool {
	if len(n.HardwareAddr) == 0 {
		return false
	}
	if n.HardwareAddr.String() == "00:00:00:00:00:00" {
		return false
	}
	// NUD_FAILED=0x20, NUD_INCOMPLETE=0x01, NUD_NONE=0x00
	if n.State == 0x20 || n.State == 0x00 || n.State == 0x01 {
		return false
	}
	return true
}

// neighStateStr converts a netlink NUD state bitmask to a readable string.
func neighStateStr(state int) string {
	switch {
	case state&0x80 != 0:
		return "PERMANENT"
	case state&0x02 != 0:
		return "REACHABLE"
	case state&0x04 != 0:
		return "STALE"
	case state&0x08 != 0:
		return "DELAY"
	case state&0x10 != 0:
		return "PROBE"
	default:
		return ""
	}
}
