//go:build freebsd

package debug

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// setDontFragment sets IP_DONTFRAG on the raw IPv4 socket on FreeBSD.
func setDontFragment(pc net.PacketConn) {
	sc, ok := pc.(syscall.Conn)
	if !ok {
		return
	}
	rawConn, err := sc.SyscallConn()
	if err != nil {
		return
	}
	rawConn.Control(func(fd uintptr) {
		unix.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_DONTFRAG, 1)
	})
}
