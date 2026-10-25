//go:build !linux

package netutil

import (
	"fmt"
	"syscall"
)

func egressControl(e Egress) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, _ syscall.RawConn) error {
		return fmt.Errorf("binding sockets to interface %q is unsupported on this platform", e.Iface)
	}
}

// BindSupported reports whether sockets can be bound to an interface.
const BindSupported = false
