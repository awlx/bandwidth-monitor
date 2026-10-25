//go:build !linux

package netutil

// ResolveEgress is Linux-only; elsewhere the plain interface binding is
// returned (which itself is unsupported, see BindSupported).
func ResolveEgress(iface string, _ bool) (Egress, error) {
	return Egress{Iface: iface}, nil
}
