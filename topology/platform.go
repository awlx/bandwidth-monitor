package topology

import "net"

// neighbor is one ARP/NDP cache entry.
type neighbor struct {
	IP        net.IP
	MAC       net.HardwareAddr
	LinkIndex int
	State     string // REACHABLE, STALE, PERMANENT, ... ("" when unknown)
	Valid     bool   // resolved, usable hardware address
}

// routePeer is a default route leaving a (point-to-point) interface together
// with that interface's remote peer addresses, if any.
type routePeer struct {
	Iface string
	Peers []net.IP
}

// wgDevice and wgPeer carry the WireGuard state topology needs.
type wgDevice struct {
	Name  string
	Peers []wgPeer
}

type wgPeer struct {
	Endpoint      net.IP // nil when the peer has no known endpoint
	AllowedIPs    []net.IPNet
	ReceiveBytes  int64
	TransmitBytes int64
}
