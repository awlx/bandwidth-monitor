//go:build !linux && !freebsd

package collector

import "log"

// linkSource is unavailable on this platform; the collector reports no interfaces.
type linkSource struct{ warned bool }

func newLinkSource() *linkSource { return &linkSource{} }

func (ls *linkSource) close() {}

func (ls *linkSource) list() (map[string]*linkInfo, bool) {
	if !ls.warned {
		log.Printf("collector: interface statistics are not supported on this platform")
		ls.warned = true
	}
	return nil, false
}

func hasDefaultRoute(string) bool { return false }
