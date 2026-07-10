package bandwidthtop

import (
	"net"
	"testing"
)

func TestMonitorServerURLExplicitServerWins(t *testing.T) {
	got, discovered := MonitorServerURL("https://monitor.example", true,
		net.ParseIP("192.0.2.1"), "eth0")
	if got != "https://monitor.example" || discovered {
		t.Fatalf("got %q discovered=%v", got, discovered)
	}
}

func TestMonitorServerURLOptOutAndNoGatewaySkip(t *testing.T) {
	if got, discovered := MonitorServerURL("", true, net.ParseIP("192.0.2.1"), "eth0"); got != "" || discovered {
		t.Fatalf("opt-out returned %q discovered=%v", got, discovered)
	}
	if got, discovered := MonitorServerURL("", false, nil, "eth0"); got != "" || discovered {
		t.Fatalf("missing gateway returned %q discovered=%v", got, discovered)
	}
}

func TestGatewayMonitorURLIPv4AndIPv6(t *testing.T) {
	tests := []struct {
		name, ip, zone, want string
	}{
		{"IPv4", "192.0.2.1", "eth0", "http://192.0.2.1:8080"},
		{"IPv6", "2001:db8::1", "eth0", "http://[2001:db8::1]:8080"},
		{"IPv6 link local", "fe80::1", "eth0", "http://[fe80::1%25eth0]:8080"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := gatewayMonitorURL(net.ParseIP(test.ip), test.zone)
			if !ok || got != test.want || !discoveryURLAllowed(got) {
				t.Fatalf("got %q ok=%v", got, ok)
			}
		})
	}
	if got, ok := gatewayMonitorURL(net.ParseIP("fe80::1"), ""); ok || got != "" {
		t.Fatalf("accepted zone-less link-local gateway %q", got)
	}
}
