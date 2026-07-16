//go:build linux

package talkers

import (
	"net"
	"testing"
)

func TestIsDefaultRoute(t *testing.T) {
	_, defaultV4, _ := net.ParseCIDR("0.0.0.0/0")
	_, subnet, _ := net.ParseCIDR("192.0.2.0/24")

	if !isDefaultRoute(nil) {
		t.Error("nil destination should represent a default route")
	}
	if !isDefaultRoute(defaultV4) {
		t.Error("0.0.0.0/0 should be a default route")
	}
	if isDefaultRoute(subnet) {
		t.Error("192.0.2.0/24 should not be a default route")
	}
}
