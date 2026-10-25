//go:build linux

package netutil

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func bindControl(iface string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var bindErr error
		if ctrlErr := c.Control(func(fd uintptr) {
			bindErr = unix.BindToDevice(int(fd), iface)
		}); ctrlErr != nil {
			return ctrlErr
		}
		return bindErr
	}
}

// BindSupported reports whether sockets can be bound to an interface.
const BindSupported = true
