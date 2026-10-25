//go:build linux

package netutil

import (
	"fmt"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// ResolveEgress reads routes, ip rules and addresses via netlink and
// returns how to send v4 or v6 host traffic out of iface. Nothing on the
// host is modified.
func ResolveEgress(iface string, v6 bool) (Egress, error) {
	fam := netlink.FAMILY_V4
	if v6 {
		fam = netlink.FAMILY_V6
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return Egress{}, fmt.Errorf("link %s: %w", iface, err)
	}
	nlRoutes, err := netlink.RouteListFiltered(fam, &netlink.Route{Table: unix.RT_TABLE_UNSPEC}, netlink.RT_FILTER_TABLE)
	if err != nil {
		return Egress{}, fmt.Errorf("list routes: %w", err)
	}
	var routes []policyRoute
	for _, r := range nlRoutes {
		if !isAll(r.Dst) || (r.Type != 0 && r.Type != unix.RTN_UNICAST) {
			continue
		}
		if r.LinkIndex > 0 {
			routes = append(routes, policyRoute{Table: r.Table, LinkIndex: r.LinkIndex})
		}
		for _, nh := range r.MultiPath {
			routes = append(routes, policyRoute{Table: r.Table, LinkIndex: nh.LinkIndex})
		}
	}
	nlRules, err := netlink.RuleList(fam)
	if err != nil {
		return Egress{}, fmt.Errorf("list rules: %w", err)
	}
	rules := make([]policyRule, 0, len(nlRules))
	for _, r := range nlRules {
		rules = append(rules, policyRule{
			Priority: r.Priority, Table: r.Table, Mark: r.Mark,
			Src: r.Src, Dst: r.Dst, IifName: r.IifName, OifName: r.OifName, Invert: r.Invert,
			Other: r.Tos != 0 || r.TunID != 0 || r.Goto > 0 || r.Flow > 0 ||
				r.SuppressIfgroup >= 0 || r.SuppressPrefixlen >= 0 ||
				r.Dport != nil || r.Sport != nil || r.IPProto != 0 || r.UIDRange != nil ||
				(r.Type != 0 && r.Type != unix.FR_ACT_TO_TBL),
		})
	}
	nlAddrs, err := netlink.AddrList(nil, fam)
	if err != nil {
		return Egress{}, fmt.Errorf("list addresses: %w", err)
	}
	addrs := make([]policyAddr, 0, len(nlAddrs))
	for _, a := range nlAddrs {
		if a.IP.IsLinkLocalUnicast() {
			continue
		}
		addrs = append(addrs, policyAddr{IP: a.IP, LinkIndex: a.LinkIndex})
	}
	return resolveEgress(iface, link.Attrs().Index, routes, rules, addrs), nil
}
