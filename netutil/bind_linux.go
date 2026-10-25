//go:build linux

package netutil

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func egressControl(e Egress) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var err error
		if ctrlErr := c.Control(func(fd uintptr) {
			if e.Iface != "" {
				err = unix.BindToDevice(int(fd), e.Iface)
			}
			if err == nil && e.Mark != 0 {
				// Needs CAP_NET_ADMIN.
				err = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, int(e.Mark))
			}
		}); ctrlErr != nil {
			return ctrlErr
		}
		return err
	}
}

// BindSupported reports whether sockets can be bound to an interface.
const BindSupported = true
