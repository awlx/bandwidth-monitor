// Package netutil provides shared IP classification helpers used across
// multiple packages (talkers, conntrack, topology, handler).
package netutil

import (
	"net"
	"time"
)

// CGNAT is the RFC 6598 Carrier-Grade NAT range (100.64.0.0/10).
var CGNAT = func() *net.IPNet {
	_, n, _ := net.ParseCIDR("100.64.0.0/10")
	return n
}()

// IsLocal returns true if ip falls within any of the given local networks.
// If localNets is empty, falls back to heuristic: RFC1918 + link-local, excluding CGNAT.
func IsLocal(ip net.IP, localNets []*net.IPNet) bool {
	if ip == nil {
		return false
	}
	if len(localNets) > 0 {
		for _, n := range localNets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	if CGNAT.Contains(ip) {
		return false
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

// IsLocalStr is a convenience wrapper that parses the IP string first.
func IsLocalStr(ipStr string, localNets []*net.IPNet) bool {
	return IsLocal(net.ParseIP(ipStr), localNets)
}

// IsGlobalUnicast returns true if the IP is globally routable unicast.
// Returns false for private, loopback, link-local, CGNAT, and ULA addresses.
func IsGlobalUnicast(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return false
	}
	if CGNAT.Contains(ip) {
		return false
	}
	// IPv6 ULA (fc00::/7)
	if len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc {
		return false
	}
	return ip.IsGlobalUnicast()
}

// DialerForInterface returns a *net.Dialer that, when iface is non-empty,
// binds outbound connections to that specific network interface via
// SO_BINDTODEVICE (Linux). Pass "" for a plain dialer that uses the OS's
// normal default routing.
//
// Binding this way relies on the interface already having its own working
// route/policy routing (as is required for it to be usable as a network
// uplink at all under normal multi-WAN operation) — this only pins the
// egress NIC, it deliberately does not add, modify, or remove any routes
// or policy rules, since that would mean silently mutating the host's
// network configuration. If the interface isn't independently routable,
// connections will fail with a clear "no route to host"/"network
// unreachable" error rather than silently using the wrong path.
func DialerForInterface(iface string) *net.Dialer {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if iface == "" {
		return d
	}
	d.Control = egressControl(Egress{Iface: iface})
	return d
}

// DialerForEgress returns a TCP dialer that sends via e (interface
// binding, firewall mark and source address); see ResolveEgress. A zero
// Egress yields a plain dialer.
func DialerForEgress(e Egress) *net.Dialer {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if e.Iface == "" && e.Mark == 0 {
		return d
	}
	d.Control = egressControl(e)
	if e.Src != nil {
		d.LocalAddr = &net.TCPAddr{IP: e.Src}
	}
	return d
}

// ListenConfigForEgress is the ListenConfig counterpart of DialerForEgress.
// Callers listen on e.Src (if set) to use the source address.
func ListenConfigForEgress(e Egress) *net.ListenConfig {
	if e.Iface == "" && e.Mark == 0 {
		return &net.ListenConfig{}
	}
	return &net.ListenConfig{Control: egressControl(e)}
}

// ListenConfigForInterface returns a *net.ListenConfig bound the same way
// as DialerForInterface, for raw/packet listeners (e.g. ICMP sockets) that
// need to originate traffic from a specific interface.
func ListenConfigForInterface(iface string) *net.ListenConfig {
	if iface == "" {
		return &net.ListenConfig{}
	}
	return &net.ListenConfig{Control: egressControl(Egress{Iface: iface})}
}
