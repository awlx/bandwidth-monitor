package topology

import "testing"

func TestParseARP(t *testing.T) {
	out := []byte(`? (192.0.2.1) at 02:00:00:00:00:01 on em0 permanent [ethernet]
? (192.0.2.2) at 02:00:00:00:00:02 on em0 expires in 1199 seconds [ethernet]
? (192.0.2.9) at (incomplete) on em0 expired [ethernet]
? (198.51.100.7) at 02:00:00:00:00:07 on igb1 expired [ethernet]
garbage line
`)
	got := parseARP(out)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	want := []struct{ ip, mac, iface, state string }{
		{"192.0.2.1", "02:00:00:00:00:01", "em0", "PERMANENT"},
		{"192.0.2.2", "02:00:00:00:00:02", "em0", "REACHABLE"},
		{"198.51.100.7", "02:00:00:00:00:07", "igb1", "STALE"},
	}
	for i, w := range want {
		g := got[i]
		if g.IP.String() != w.ip || g.MAC.String() != w.mac || g.Iface != w.iface || g.State != w.state {
			t.Errorf("entry %d = %+v, want %+v", i, g, w)
		}
	}

	local := map[string]bool{"192.0.2.1": true}
	if bsdNeighValid(got[0], local) {
		t.Error("own address should be invalid")
	}
	if !bsdNeighValid(got[1], local) {
		t.Error("reachable neighbor should be valid")
	}
}

func TestParseNDP(t *testing.T) {
	out := []byte(`Neighbor                             Linklayer Address  Netif Expire    S Flags
fe80::2%em0                          02:00:00:00:00:02 em0 23h53m25s S R
2001:db8::1                          02:00:00:00:00:01 em0 permanent R 
2001:db8::5                          02:00:00:00:00:05 em0 4s        R 
2001:db8::9                          (incomplete)      em0 expired   I 
`)
	got := parseNDP(out)
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(got), got)
	}
	if got[0].IP.String() != "fe80::2" || got[0].State != "STALE" || got[0].Iface != "em0" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].State != "PERMANENT" || got[2].State != "REACHABLE" {
		t.Errorf("states = %q, %q", got[1].State, got[2].State)
	}
}

func TestParseIfconfigVLANParents(t *testing.T) {
	out := []byte(`em1: flags=1008843<UP,BROADCAST,RUNNING,SIMPLEX,MULTICAST,LOWER_UP> metric 0 mtu 1500
	ether 02:00:00:00:00:01
vlan5: flags=1008842<BROADCAST,RUNNING,SIMPLEX,MULTICAST,LOWER_UP> metric 0 mtu 1500
	groups: vlan
	vlan: 5 vlanproto: 802.1q vlanpcp: 0 parent interface: em1
em1_vlan10: flags=1008843<UP> metric 0 mtu 1500
	vlan: 10 vlanproto: 802.1q vlanpcp: 0 parent interface: em1
vlan9: flags=8842<BROADCAST> metric 0 mtu 1500
	vlan: 0 vlanproto: 802.1q vlanpcp: 0 parent interface: <none>
`)
	got := parseIfconfigVLANParents(out)
	if len(got) != 2 || got["vlan5"] != "em1" || got["em1_vlan10"] != "em1" {
		t.Errorf("got %v", got)
	}
}

func TestParseWGShow(t *testing.T) {
	ep := []byte("wg0\tPEER1=\t203.0.113.5:51820\nwg0\tPEER2=\t(none)\nwg1\tPEER3=\t[2001:db8::5]:51820\n")
	aips := []byte("wg0\tPEER1=\t10.9.0.0/24 0.0.0.0/0\nwg0\tPEER2=\t(none)\nwg1\tPEER3=\t10.10.0.2/32\n")
	tr := []byte("wg0\tPEER1=\t100\t200\nwg0\tPEER2=\t0\t0\nwg1\tPEER3=\t5\t6\n")

	devs := parseWGShow(ep, aips, tr)
	if len(devs) != 2 || devs[0].Name != "wg0" || devs[1].Name != "wg1" {
		t.Fatalf("devices = %+v", devs)
	}
	p1 := devs[0].Peers[0]
	if p1.Endpoint.String() != "203.0.113.5" || len(p1.AllowedIPs) != 2 || p1.ReceiveBytes != 100 || p1.TransmitBytes != 200 {
		t.Errorf("peer1 = %+v", p1)
	}
	if ones, _ := p1.AllowedIPs[1].Mask.Size(); ones != 0 {
		t.Errorf("expected default route in allowed IPs, got %v", p1.AllowedIPs)
	}
	if devs[0].Peers[1].Endpoint != nil || len(devs[0].Peers[1].AllowedIPs) != 0 {
		t.Errorf("peer2 = %+v", devs[0].Peers[1])
	}
	if devs[1].Peers[0].Endpoint.String() != "2001:db8::5" {
		t.Errorf("peer3 = %+v", devs[1].Peers[0])
	}
}
