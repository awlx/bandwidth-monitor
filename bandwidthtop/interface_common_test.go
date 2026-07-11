//go:build linux || darwin

package bandwidthtop

import (
	"net"
	"testing"
)

type commonTestAddr string

func (a commonTestAddr) Network() string { return "test" }
func (a commonTestAddr) String() string  { return string(a) }

func TestAssignedLocalNetworksAreDeterministicAcrossPlatforms(t *testing.T) {
	first := []net.Addr{
		commonTestAddr("2001:db8:2::2/64"),
		commonTestAddr("192.0.2.20/24"),
		commonTestAddr("fe80::1%test0/64"),
		commonTestAddr("192.0.2.10/24"),
	}
	second := []net.Addr{first[3], first[2], first[0], first[1]}
	firstNetworks, firstLocal := assignedLocalNetworks(first)
	secondNetworks, secondLocal := assignedLocalNetworks(second)
	if firstLocal != "192.0.2.10" || secondLocal != firstLocal ||
		len(firstNetworks) != len(secondNetworks) {
		t.Fatalf("first=%q %v second=%q %v", firstLocal, firstNetworks, secondLocal, secondNetworks)
	}
	for index := range firstNetworks {
		if firstNetworks[index].String() != secondNetworks[index].String() {
			t.Fatalf("network order differs: %v vs %v", firstNetworks, secondNetworks)
		}
	}
}
