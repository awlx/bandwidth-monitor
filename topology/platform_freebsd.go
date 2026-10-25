//go:build freebsd

package topology

import (
	"context"
	"net"
	"os"
	"os/exec"
	"time"

	"bandwidth-monitor/bsdnet"
)

const cmdTimeout = 10 * time.Second

func runCmd(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func localIPSet() map[string]bool {
	m := make(map[string]bool)
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			m[n.IP.String()] = true
		}
	}
	return m
}

// listNeighbors returns the ARP (IPv4) or NDP (IPv6) cache via arp(8)/ndp(8).
func listNeighbors(ipv6 bool) ([]neighbor, error) {
	var (
		out []byte
		err error
	)
	if ipv6 {
		out, err = runCmd("/usr/sbin/ndp", "-an")
	} else {
		out, err = runCmd("/usr/sbin/arp", "-an")
	}
	if err != nil {
		return nil, err
	}
	var parsed []bsdNeighbor
	if ipv6 {
		parsed = parseNDP(out)
	} else {
		parsed = parseARP(out)
	}

	local := localIPSet()
	idx := make(map[string]int)
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			idx[i.Name] = i.Index
		}
	}
	res := make([]neighbor, 0, len(parsed))
	for _, n := range parsed {
		res = append(res, neighbor{
			IP:        n.IP,
			MAC:       n.MAC,
			LinkIndex: idx[n.Iface],
			State:     n.State,
			Valid:     bsdNeighValid(n, local),
		})
	}
	return res, nil
}

// defaultRoutePeers returns IPv4 default routes. On FreeBSD a PPP/PPPoE
// default route's gateway is the remote peer address.
func defaultRoutePeers() ([]routePeer, error) {
	routes, err := bsdnet.DefaultRoutes()
	if err != nil {
		return nil, err
	}
	names := ifaceIndexNames()
	var out []routePeer
	for _, r := range routes {
		if r.IPv6 {
			continue
		}
		rp := routePeer{Iface: names[r.Index]}
		if r.Gateway != nil && isPointToPoint(r.Index) {
			rp.Peers = []net.IP{r.Gateway}
		}
		out = append(out, rp)
	}
	return out, nil
}

func isPointToPoint(index int) bool {
	ifc, err := net.InterfaceByIndex(index)
	return err == nil && ifc.Flags&net.FlagPointToPoint != 0
}

// defaultGatewayIP returns the IPv4 default gateway from the routing table.
func defaultGatewayIP() string {
	routes, err := bsdnet.DefaultRoutes()
	if err != nil {
		return ""
	}
	for _, r := range routes {
		if !r.IPv6 && r.Gateway != nil {
			return r.Gateway.String()
		}
	}
	return ""
}

// ifaceIndexNames builds a map from interface index to name.
func ifaceIndexNames() map[int]string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	m := make(map[int]string, len(ifs))
	for _, i := range ifs {
		m[i.Index] = i.Name
	}
	return m
}

// buildVLANParentMap maps VLAN interfaces to their parent via ifconfig(8).
func buildVLANParentMap() map[string]string {
	out, err := runCmd("/sbin/ifconfig", "-a")
	if err != nil {
		return nil
	}
	return parseIfconfigVLANParents(out)
}

// wireGuardDevices reads WireGuard peers via wg(8), which is part of the
// FreeBSD 14+ base system and OPNsense. The per-field "show" commands are
// used instead of "dump" so private keys are never read.
func wireGuardDevices() ([]wgDevice, error) {
	wg := "/usr/bin/wg"
	if _, err := os.Stat(wg); err != nil {
		if wg, err = exec.LookPath("wg"); err != nil {
			return nil, err
		}
	}
	ep, err := runCmd(wg, "show", "all", "endpoints")
	if err != nil {
		return nil, err
	}
	aips, err := runCmd(wg, "show", "all", "allowed-ips")
	if err != nil {
		return nil, err
	}
	tr, err := runCmd(wg, "show", "all", "transfer")
	if err != nil {
		return nil, err
	}
	return parseWGShow(ep, aips, tr), nil
}
