//go:build linux

package bandwidthtop

import (
	"strings"
	"testing"
)

func TestDefaultInterfaceChoosesLowestMetricUpRoute(t *testing.T) {
	table := `Iface Destination Gateway Flags RefCnt Use Metric Mask
eth1 00000000 010200C0 0003 0 0 200 00000000
eth0 00000000 010200C0 0003 0 0 10 00000000
eth2 00000000 010200C0 0000 0 0 1 00000000
`
	got, err := defaultInterface(strings.NewReader(table))
	if err != nil || got != "eth0" {
		t.Fatalf("got %q, %v", got, err)
	}
}
