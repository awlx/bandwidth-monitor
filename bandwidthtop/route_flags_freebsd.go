//go:build freebsd

package bandwidthtop

// FreeBSD has no interface-scoped routes. It uses FIBs instead, and the
// routing socket returns the routes of the FIB of the process.
const routeFlagIfScope = 0
