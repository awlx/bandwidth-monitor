//go:build darwin

package bandwidthtop

import "golang.org/x/sys/unix"

// routeFlagIfScope marks Darwin interface-scoped routes. These are not the
// primary default route, so automatic selection skips them.
const routeFlagIfScope = unix.RTF_IFSCOPE
