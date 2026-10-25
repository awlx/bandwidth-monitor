package collector

import (
	"testing"

	"bandwidth-monitor/bsdnet"
)

func TestBSDLinkKind(t *testing.T) {
	cases := []struct {
		name        string
		typ         uint8
		kind, encap string
	}{
		{"igb0", bsdnet.TypeEther, "", "ether"},
		{"vtnet1", bsdnet.TypeEther, "", "ether"},
		{"lo0", bsdnet.TypeLoop, "", "loopback"},
		{"pflog0", bsdnet.TypePflog, "", "loopback"},
		{"enc0", bsdnet.TypeEnc, "", "loopback"},
		{"wg0", bsdnet.TypeWireGuard, "wireguard", ""},
		{"vlan5", bsdnet.TypeL2VLAN, "vlan", ""},
		{"igb1_vlan10", bsdnet.TypeEther, "vlan", ""},
		{"bridge0", bsdnet.TypeEther, "bridge", ""},
		{"lagg0", bsdnet.TypeEther, "bond", ""},
		{"pppoe0", bsdnet.TypePPP, "ppp", "ppp"},
		{"gif0", bsdnet.TypeGIF, "gre", ""},
		{"ovpns1", 0x35, "tun", ""},
		{"tun0", 0x35, "tun", ""},
		{"vxlan0", bsdnet.TypeEther, "vxlan", ""},
	}
	for _, c := range cases {
		k, e := bsdLinkKind(c.name, c.typ)
		if k != c.kind || e != c.encap {
			t.Errorf("%s: got (%q,%q) want (%q,%q)", c.name, k, e, c.kind, c.encap)
		}
	}
}
