package talkers

import (
	"net"
	"testing"
)

func mustCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", cidr, err)
	}
	return n
}

func TestIsLANAddress(t *testing.T) {
	publicLAN := []*net.IPNet{
		mustCIDR(t, "195.114.13.0/24"),
		mustCIDR(t, "2001:678:ac8::/48"),
	}

	tests := []struct {
		name      string
		ip        string
		localNets []*net.IPNet
		want      bool
	}{
		{"public v4 inside LOCAL_NETS", "195.114.13.5", publicLAN, true},
		{"public v6 inside LOCAL_NETS", "2001:678:ac8:1::10", publicLAN, true},
		{"public v4 outside LOCAL_NETS", "8.8.8.8", publicLAN, false},
		{"private v4 outside LOCAL_NETS", "192.168.1.1", publicLAN, false},
		{"private v4 without LOCAL_NETS", "192.168.1.1", nil, true},
		{"ULA v6 without LOCAL_NETS", "fd00::1", nil, true},
		{"public v4 without LOCAL_NETS", "195.114.13.5", nil, false},
		{"CGNAT without LOCAL_NETS", "100.64.0.1", nil, false},
		{"link-local v6 never LAN", "fe80::1", nil, false},
		{"link-local v6 never LAN even in LOCAL_NETS", "fe80::1", []*net.IPNet{mustCIDR(t, "fe80::/10")}, false},
		{"nil IP", "", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if got := isLANAddress(ip, tt.localNets); got != tt.want {
				t.Errorf("isLANAddress(%q, localNets=%v) = %v, want %v", tt.ip, tt.localNets, got, tt.want)
			}
		})
	}
}
