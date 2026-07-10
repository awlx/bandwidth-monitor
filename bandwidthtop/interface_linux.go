//go:build linux

package bandwidthtop

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

func SelectInterface(name string) (*net.Interface, []*net.IPNet, string, error) {
	if name == "" {
		f, err := os.Open("/proc/net/route")
		if err != nil {
			return nil, nil, "", err
		}
		name, err = defaultInterface(f)
		f.Close()
		if err != nil {
			return nil, nil, "", err
		}
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, nil, "", fmt.Errorf("interface %q does not exist", name)
	}
	if iface.Flags&net.FlagUp == 0 {
		return nil, nil, "", fmt.Errorf("interface %q is down", name)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, nil, "", err
	}
	var nets []*net.IPNet
	local := ""
	for _, addr := range addrs {
		ip, network, err := net.ParseCIDR(addr.String())
		if err != nil {
			continue
		}
		network.IP = ip
		nets = append(nets, network)
		if local == "" && !ip.IsLinkLocalUnicast() {
			local = ip.String()
		}
	}
	if local == "" {
		return nil, nil, "", fmt.Errorf("interface %q has no usable IP address", name)
	}
	return iface, nets, local, nil
}

func defaultInterface(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return "", errorsNew("empty route table")
	}
	best, bestMetric := "", int64(^uint64(0)>>1)
	for scanner.Scan() {
		f := strings.Fields(scanner.Text())
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, err1 := strconv.ParseUint(f[3], 16, 32)
		metric, err2 := strconv.ParseInt(f[6], 10, 64)
		if err1 != nil || err2 != nil || flags&1 == 0 {
			continue
		}
		if metric < bestMetric {
			best, bestMetric = f[0], metric
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if best == "" {
		return "", errorsNew("no default-route interface found")
	}
	return best, nil
}

func errorsNew(s string) error { return fmt.Errorf("%s", s) }
