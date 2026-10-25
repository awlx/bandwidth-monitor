//go:build !linux && !freebsd

package topology

import (
	"errors"
	"net"
)

var errUnsupported = errors.New("topology: neighbor discovery not supported on this platform")

func listNeighbors(bool) ([]neighbor, error)  { return nil, errUnsupported }
func defaultRoutePeers() ([]routePeer, error) { return nil, errUnsupported }
func wireGuardDevices() ([]wgDevice, error)   { return nil, errUnsupported }
func defaultGatewayIP() string                { return "" }
func buildVLANParentMap() map[string]string   { return nil }

func ifaceIndexNames() map[int]string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	m := make(map[int]string, len(ifs))
	for _, i := range ifs {
		m[i.Index] = i.Name
	}
	return m
}
