//go:build linux || darwin

package bandwidthtop

import (
	"bytes"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

type CaptureInterface struct {
	Interface *net.Interface
	LocalNets []*net.IPNet
	LocalIP   string
	Gateway   net.IP
}

func SelectInterface(name string) (*net.Interface, []*net.IPNet, string, error) {
	selected, err := SelectCaptureInterface(name)
	if err != nil {
		return nil, nil, "", err
	}
	return selected.Interface, selected.LocalNets, selected.LocalIP, nil
}

func SelectCaptureInterface(name string) (*CaptureInterface, error) {
	iface, gateway, err := selectCaptureTarget(name)
	if err != nil {
		return nil, err
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("interface %q is down", iface.Name)
	}
	if iface.Flags&net.FlagLoopback != 0 {
		return nil, fmt.Errorf("interface %q is loopback and cannot be used for capture", iface.Name)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("list addresses for interface %q: %w", iface.Name, err)
	}
	nets, local := assignedLocalNetworks(addrs)
	if local == "" {
		return nil, fmt.Errorf("interface %q has no usable IP address", iface.Name)
	}

	return &CaptureInterface{
		Interface: iface,
		LocalNets: nets,
		LocalIP:   local,
		Gateway:   gateway,
	}, nil
}

func assignedLocalNetworks(addrs []net.Addr) ([]*net.IPNet, string) {
	type assignedNetwork struct {
		ip      net.IP
		network *net.IPNet
	}
	var assigned []assignedNetwork
	for _, addr := range addrs {
		ip, mask := assignedIPAndMask(addr)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() {
			continue
		}
		ones, bits := mask.Size()
		if bits == 0 || ones < 0 {
			continue
		}
		if ip.To4() != nil {
			if bits != 32 {
				continue
			}
			ip = ip.To4()
		} else {
			if bits != 128 {
				continue
			}
			ip = ip.To16()
		}
		if ip == nil {
			continue
		}
		assigned = append(assigned, assignedNetwork{
			ip:      ip,
			network: &net.IPNet{IP: ip.Mask(mask), Mask: append(net.IPMask(nil), mask...)},
		})
	}
	sort.Slice(assigned, func(i, j int) bool {
		left4, right4 := assigned[i].ip.To4(), assigned[j].ip.To4()
		if (left4 != nil) != (right4 != nil) {
			return left4 != nil
		}
		if comparison := bytes.Compare(assigned[i].ip, assigned[j].ip); comparison != 0 {
			return comparison < 0
		}
		leftPrefix, _ := assigned[i].network.Mask.Size()
		rightPrefix, _ := assigned[j].network.Mask.Size()
		return leftPrefix < rightPrefix
	})
	networks := make([]*net.IPNet, 0, len(assigned))
	local := ""
	for _, entry := range assigned {
		networks = append(networks, entry.network)
		if local == "" && !entry.ip.IsLinkLocalUnicast() {
			local = entry.ip.String()
		}
	}
	if local == "" && len(assigned) > 0 {
		local = assigned[0].ip.String()
	}
	return networks, local
}

func assignedIPAndMask(addr net.Addr) (net.IP, net.IPMask) {
	if network, ok := addr.(*net.IPNet); ok {
		return append(net.IP(nil), network.IP...), append(net.IPMask(nil), network.Mask...)
	}
	raw := addr.String()
	slash := strings.LastIndexByte(raw, '/')
	if slash < 0 {
		return nil, nil
	}
	host := raw[:slash]
	if zone := strings.LastIndexByte(host, '%'); zone >= 0 {
		host = host[:zone]
	}
	ip := net.ParseIP(host)
	prefix, err := strconv.Atoi(raw[slash+1:])
	if err != nil || ip == nil {
		return nil, nil
	}
	bits := 128
	if ip.To4() != nil {
		bits = 32
	}
	if prefix < 0 || prefix > bits {
		return nil, nil
	}
	return ip, net.CIDRMask(prefix, bits)
}
