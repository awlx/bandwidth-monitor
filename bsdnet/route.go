package bsdnet

import "net"

// DefaultRoute is a 0.0.0.0/0 or ::/0 route and the interface it leaves by.
type DefaultRoute struct {
	Index   int    // outgoing interface index
	Gateway net.IP // nil for interface (point-to-point) routes
	IPv6    bool
}
