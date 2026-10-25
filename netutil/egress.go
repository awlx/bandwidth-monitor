package netutil

import (
	"fmt"
	"net"
	"sort"
)

// Egress describes how host-originated traffic is sent out of one WAN
// interface on a policy-routed multi-WAN host: the socket is bound to
// Iface and, where the WAN's policy routing table is only selected by an
// ip rule, carries that rule's firewall mark (SO_MARK) or source address.
type Egress struct {
	Iface string
	Mark  uint32 // SO_MARK; 0 = none
	Src   net.IP // local source address; nil = any
	// Unroutable is non-empty when no route or ip rule lets traffic from
	// this host leave via Iface; probes must not be sent.
	Unroutable string
}

// Equal reports whether e and o select the same egress.
func (e Egress) Equal(o Egress) bool {
	return e.Iface == o.Iface && e.Mark == o.Mark && e.Src.Equal(o.Src) && e.Unroutable == o.Unroutable
}

func (e Egress) String() string {
	switch {
	case e.Unroutable != "":
		return "unroutable: " + e.Unroutable
	case e.Mark != 0 && e.Src != nil:
		return fmt.Sprintf("fwmark %#x, source %s", e.Mark, e.Src)
	case e.Mark != 0:
		return fmt.Sprintf("fwmark %#x", e.Mark)
	case e.Src != nil:
		return "source " + e.Src.String()
	}
	return "bound to interface"
}

// policyRoute is a default route (any table) out of LinkIndex.
type policyRoute struct {
	Table     int
	LinkIndex int
}

// policyRule is an ip rule. Other is set when the rule has selectors this
// resolver does not model (ports, protocol, uid, tos, suppress_*, goto,
// non-lookup action, ...); such rules are never used.
type policyRule struct {
	Priority         int
	Table            int
	Mark             uint32
	Src, Dst         *net.IPNet
	IifName, OifName string
	Invert           bool
	Other            bool
}

type policyAddr struct {
	IP        net.IP
	LinkIndex int
}

func isAll(n *net.IPNet) bool {
	if n == nil {
		return true
	}
	ones, _ := n.Mask.Size()
	return ones == 0
}

// resolveEgress picks how a socket bound to iface reaches a default route
// via iface: it finds the tables holding such a route and the ip rules
// that host traffic bound to iface can satisfy and that select one of
// them. A rule without mark or source selector (e.g. the main table rule)
// needs nothing beyond the interface binding and is preferred; otherwise
// the highest-priority fwmark rule adds SO_MARK, or a from-rule a local
// source address in the rule's prefix.
func resolveEgress(iface string, ifindex int, routes []policyRoute, rules []policyRule, addrs []policyAddr) Egress {
	tables := map[int]bool{}
	for _, r := range routes {
		if r.LinkIndex == ifindex {
			tables[r.Table] = true
		}
	}
	if len(tables) == 0 {
		return Egress{Iface: iface, Unroutable: "no default route via " + iface}
	}
	sorted := make([]policyRule, 0, len(rules))
	for _, r := range rules {
		// Locally generated traffic matches "iif lo"; a bound socket
		// matches "oif <iface>".
		if !tables[r.Table] || r.Other || r.Invert || !isAll(r.Dst) ||
			(r.IifName != "" && r.IifName != "lo") || (r.OifName != "" && r.OifName != iface) {
			continue
		}
		sorted = append(sorted, r)
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })
	// A plain bound socket (no mark, kernel-chosen source) cannot match
	// mark or source rules, so if any rule without them selects a table
	// with the default route, no extra socket options are needed. Lookups
	// of other tables fail for a socket bound to iface and fall through.
	for _, r := range sorted {
		if r.Mark == 0 && isAll(r.Src) {
			return Egress{Iface: iface}
		}
	}
	for _, r := range sorted {
		e := Egress{Iface: iface, Mark: r.Mark}
		if !isAll(r.Src) {
			e.Src = pickSource(r.Src, ifindex, addrs)
			if e.Src == nil {
				continue
			}
		}
		return e
	}
	return Egress{Iface: iface, Unroutable: "default route via " + iface + " is only in a policy table that no ip rule selects for host traffic"}
}

// pickSource returns a local address inside n, preferring one on ifindex.
func pickSource(n *net.IPNet, ifindex int, addrs []policyAddr) net.IP {
	var other net.IP
	for _, a := range addrs {
		if !n.Contains(a.IP) {
			continue
		}
		if a.LinkIndex == ifindex {
			return a.IP
		}
		if other == nil {
			other = a.IP
		}
	}
	return other
}
