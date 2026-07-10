package bandwidthtop

import (
	"fmt"
	"net"
	"strings"
)

// LocalNetworkFlags collects repeatable, strictly parsed local CIDR overrides.
type LocalNetworkFlags struct {
	networks []*net.IPNet
}

func (f *LocalNetworkFlags) String() string {
	values := make([]string, 0, len(f.networks))
	for _, network := range f.networks {
		values = append(values, network.String())
	}
	return strings.Join(values, ",")
}

func (f *LocalNetworkFlags) Set(value string) error {
	ip, network, err := net.ParseCIDR(value)
	if err != nil {
		return fmt.Errorf("invalid local network %q: %w", value, err)
	}
	if ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("invalid local network %q: prefix must identify unicast addresses", value)
	}
	network.IP = ip.Mask(network.Mask)
	ones, _ := network.Mask.Size()
	if ones == 0 || network.IP.IsUnspecified() || network.IP.IsMulticast() {
		return fmt.Errorf("invalid local network %q: prefix must not cover all or non-unicast addresses", value)
	}
	f.networks = append(f.networks, network)
	return nil
}

func (f *LocalNetworkFlags) Networks() []*net.IPNet {
	return copyNetworks(f.networks)
}

// EffectiveLocalNetworks replaces interface-derived prefixes when at least one
// explicit override is supplied.
func EffectiveLocalNetworks(interfaceNetworks, overrideNetworks []*net.IPNet) []*net.IPNet {
	if len(overrideNetworks) > 0 {
		return copyNetworks(overrideNetworks)
	}
	return copyNetworks(interfaceNetworks)
}

func copyNetworks(networks []*net.IPNet) []*net.IPNet {
	copied := make([]*net.IPNet, 0, len(networks))
	for _, network := range networks {
		if network == nil {
			continue
		}
		copied = append(copied, &net.IPNet{
			IP:   append(net.IP(nil), network.IP...),
			Mask: append(net.IPMask(nil), network.Mask...),
		})
	}
	return copied
}
