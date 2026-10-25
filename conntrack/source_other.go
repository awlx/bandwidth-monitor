//go:build !linux && !freebsd

package conntrack

import "log"

// stateSource is unavailable on this platform.
type stateSource struct{}

func newStateSource() *stateSource { return &stateSource{} }

func (s *stateSource) probe() bool {
	log.Println("conntrack: connection tracking is not supported on this platform")
	return false
}

func (s *stateSource) close() {}

func (s *stateSource) dump(func(Entry)) (count, max int, ok bool) { return 0, 0, false }
