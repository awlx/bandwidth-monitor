//go:build !linux

package netutil

import (
	"fmt"
	"syscall"
)

func bindControl(iface string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, _ syscall.RawConn) error {
		return fmt.Errorf("binding sockets to interface %q is unsupported on this platform", iface)
	}
}
