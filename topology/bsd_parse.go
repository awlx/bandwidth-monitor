package topology

import (
	"bufio"
	"bytes"
	"net"
	"strconv"
	"strings"
)

// The parsers below decode the output of FreeBSD's arp(8), ndp(8),
// ifconfig(8) and wg(8). They are kept free of build tags so they can be
// unit-tested on any platform.

// bsdNeighbor is a parsed arp/ndp entry, keyed by interface name.
type bsdNeighbor struct {
	IP    net.IP
	MAC   net.HardwareAddr
	Iface string
	State string
}

// parseARP parses `arp -an` output, e.g.
//
//	? (192.0.2.2) at 02:00:00:00:00:02 on em0 expires in 1199 seconds [ethernet]
//	? (192.0.2.1) at 02:00:00:00:00:01 on em0 permanent [ethernet]
//	? (192.0.2.9) at (incomplete) on em0 expired [ethernet]
func parseARP(out []byte) []bsdNeighbor {
	var res []bsdNeighbor
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// ? (ip) at mac on iface ...
		if len(f) < 6 || f[2] != "at" || f[4] != "on" {
			continue
		}
		ip := net.ParseIP(strings.Trim(f[1], "()"))
		mac, err := net.ParseMAC(f[3])
		if ip == nil || err != nil {
			continue
		}
		state := "REACHABLE"
		rest := strings.Join(f[6:], " ")
		switch {
		case strings.HasPrefix(rest, "permanent"):
			state = "PERMANENT"
		case strings.HasPrefix(rest, "expired"):
			state = "STALE"
		}
		res = append(res, bsdNeighbor{IP: ip, MAC: mac, Iface: f[5], State: state})
	}
	return res
}

// parseNDP parses `ndp -an` output, e.g.
//
//	Neighbor                 Linklayer Address  Netif Expire    S Flags
//	fe80::2%em0              02:00:00:00:00:02  em0   23h59m50s S R
//	2001:db8::1              02:00:00:00:00:01  em0   permanent R
//	2001:db8::9              (incomplete)       em0   expired   I
func parseNDP(out []byte) []bsdNeighbor {
	var res []bsdNeighbor
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 5 || f[0] == "Neighbor" {
			continue
		}
		host := f[0]
		if i := strings.IndexByte(host, '%'); i >= 0 {
			host = host[:i]
		}
		ip := net.ParseIP(host)
		mac, err := net.ParseMAC(f[1])
		if ip == nil || err != nil {
			continue
		}
		var state string
		if f[3] == "permanent" {
			state = "PERMANENT"
		} else {
			switch f[4] {
			case "R":
				state = "REACHABLE"
			case "S":
				state = "STALE"
			case "D":
				state = "DELAY"
			case "P":
				state = "PROBE"
			case "I", "N", "W":
				state = "INCOMPLETE"
			}
		}
		res = append(res, bsdNeighbor{IP: ip, MAC: mac, Iface: f[2], State: state})
	}
	return res
}

// bsdNeighValid mirrors the Linux filter: a usable, resolved hardware
// address that does not belong to one of our own interfaces (FreeBSD lists
// local addresses as permanent entries, Linux does not list them at all).
func bsdNeighValid(n bsdNeighbor, localIPs map[string]bool) bool {
	if len(n.MAC) == 0 || n.MAC.String() == "00:00:00:00:00:00" {
		return false
	}
	if n.State == "INCOMPLETE" || n.State == "" {
		return false
	}
	return !localIPs[n.IP.String()]
}

// parseIfconfigVLANParents extracts VLAN parents from `ifconfig -a`, e.g.
//
//	vlan5: flags=...
//		vlan: 5 vlanproto: 802.1q vlanpcp: 0 parent interface: em1
func parseIfconfigVLANParents(out []byte) map[string]string {
	m := make(map[string]string)
	var cur string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if line != "" && line[0] != '\t' && line[0] != ' ' {
			if i := strings.Index(line, ": flags="); i > 0 {
				cur = line[:i]
			}
			continue
		}
		t := strings.TrimSpace(line)
		if cur == "" || !strings.HasPrefix(t, "vlan:") {
			continue
		}
		if i := strings.Index(t, "parent interface:"); i >= 0 {
			if p := strings.TrimSpace(t[i+len("parent interface:"):]); p != "" && p != "<none>" {
				m[cur] = strings.Fields(p)[0]
			}
		}
	}
	return m
}

// parseWGShow merges the outputs of `wg show all endpoints`,
// `wg show all allowed-ips` and `wg show all transfer` (each line is
// "iface<TAB>peer-public-key<TAB>value...") into devices. Interfaces without
// peers are omitted.
func parseWGShow(endpoints, allowedIPs, transfer []byte) []wgDevice {
	type key struct{ iface, peer string }
	peers := make(map[key]*wgPeer)
	var order []key
	get := func(iface, peer string) *wgPeer {
		k := key{iface, peer}
		p, ok := peers[k]
		if !ok {
			p = &wgPeer{}
			peers[k] = p
			order = append(order, k)
		}
		return p
	}
	each := func(out []byte, fn func(p *wgPeer, vals []string)) {
		sc := bufio.NewScanner(bytes.NewReader(out))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 3 {
				continue
			}
			fn(get(f[0], f[1]), f[2:])
		}
	}
	each(endpoints, func(p *wgPeer, v []string) {
		if host, _, err := net.SplitHostPort(v[0]); err == nil {
			p.Endpoint = net.ParseIP(host)
		}
	})
	each(allowedIPs, func(p *wgPeer, v []string) {
		for _, s := range v {
			if _, n, err := net.ParseCIDR(s); err == nil {
				p.AllowedIPs = append(p.AllowedIPs, *n)
			}
		}
	})
	each(transfer, func(p *wgPeer, v []string) {
		if len(v) < 2 {
			return
		}
		p.ReceiveBytes, _ = strconv.ParseInt(v[0], 10, 64)
		p.TransmitBytes, _ = strconv.ParseInt(v[1], 10, 64)
	})

	var devs []wgDevice
	idx := make(map[string]int)
	for _, k := range order {
		i, ok := idx[k.iface]
		if !ok {
			i = len(devs)
			idx[k.iface] = i
			devs = append(devs, wgDevice{Name: k.iface})
		}
		devs[i].Peers = append(devs[i].Peers, *peers[k])
	}
	return devs
}
