package bandwidthtop

import (
	"net"
	"testing"
)

func TestLocalNetworkFlagsAreRepeatableAndStrict(t *testing.T) {
	var flags LocalNetworkFlags
	if err := flags.Set("192.168.50.7/24"); err != nil {
		t.Fatal(err)
	}
	if err := flags.Set("2001:db8:1::7/64"); err != nil {
		t.Fatal(err)
	}
	got := flags.Networks()
	if len(got) != 2 || got[0].String() != "192.168.50.0/24" ||
		got[1].String() != "2001:db8:1::/64" {
		t.Fatalf("unexpected normalized networks: %v", got)
	}
	for _, invalid := range []string{
		"", "192.168.1.0/33", "::/0", "2001:db8::1/0", "0.0.0.1/0", "ff00::/8",
	} {
		if err := flags.Set(invalid); err == nil {
			t.Fatalf("accepted invalid local network %q", invalid)
		}
	}
}

func TestLocalNetworkOverridesReplaceInterfacePrefixes(t *testing.T) {
	_, interfaceNetwork, _ := net.ParseCIDR("192.168.1.0/24")
	_, override, _ := net.ParseCIDR("10.20.0.0/16")
	got := EffectiveLocalNetworks([]*net.IPNet{interfaceNetwork}, []*net.IPNet{override})
	if len(got) != 1 || !got[0].Contains(net.ParseIP("10.20.1.1")) ||
		got[0].Contains(net.ParseIP("192.168.1.20")) {
		t.Fatalf("override did not replace interface prefixes: %v", got)
	}
	got = EffectiveLocalNetworks([]*net.IPNet{interfaceNetwork}, nil)
	if len(got) != 1 || !got[0].Contains(net.ParseIP("192.168.1.20")) {
		t.Fatalf("interface prefix was not retained without override: %v", got)
	}
}
