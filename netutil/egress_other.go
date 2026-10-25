//go:build !linux

package netutil

import "net"

// ResolveEgress is Linux-only; elsewhere the plain interface binding is
// returned (which itself is unsupported, see BindSupported).
func ResolveEgress(iface string, _ bool) (Egress, error) {
	return Egress{Iface: iface}, nil
}

// SameRouteAsDefault is Linux-only; elsewhere paths are never merged.
func SameRouteAsDefault(Egress, net.IP) (bool, error) { return false, nil }
