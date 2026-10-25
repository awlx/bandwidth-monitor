package collector

import (
	"strings"

	"bandwidth-monitor/bsdnet"
)

// bsdLinkKind maps a FreeBSD interface name and if_type to the Linux-style
// link kind and encapsulation that classifyLinkUncached understands.
func bsdLinkKind(name string, ifType uint8) (kind, encap string) {
	switch ifType {
	case bsdnet.TypeLoop, bsdnet.TypeEnc, bsdnet.TypePflog, bsdnet.TypePfsync:
		return "", "loopback"
	case bsdnet.TypeWireGuard:
		return "wireguard", ""
	case bsdnet.TypeL2VLAN:
		return "vlan", ""
	}
	driver := strings.ToLower(strings.TrimRight(name, "0123456789"))
	switch {
	case strings.Contains(name, "_vlan") || strings.HasPrefix(driver, "vlan") || strings.HasPrefix(driver, "qinq"):
		return "vlan", ""
	case driver == "wg":
		return "wireguard", ""
	case driver == "bridge":
		return "bridge", ""
	case driver == "lagg":
		return "bond", ""
	case driver == "pppoe" || driver == "pptp" || driver == "l2tp" || driver == "ppp":
		return "ppp", "ppp"
	case driver == "gif" || driver == "gre" || driver == "ipsec":
		return "gre", ""
	case driver == "tun" || driver == "tap" || driver == "ovpns" || driver == "ovpnc" || driver == "ovpn":
		return "tun", ""
	case driver == "vxlan":
		return "vxlan", ""
	case driver == "lo" || driver == "pflog" || driver == "pfsync" || driver == "enc":
		return "", "loopback"
	}
	if ifType == bsdnet.TypePPP {
		return "ppp", "ppp"
	}
	if ifType == bsdnet.TypeGIF {
		return "gre", ""
	}
	return "", "ether"
}
