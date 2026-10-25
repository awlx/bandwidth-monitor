//go:build !linux && !freebsd

package debug

import "net"

// setDontFragment is a no-op on platforms without a DF socket option.
// On macOS the DF bit is typically set by default for ICMP echo,
// and the "message too long" error from WriteTo covers local MTU.
func setDontFragment(_ net.PacketConn) {}
