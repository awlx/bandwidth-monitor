//go:build freebsd

package conntrack

import (
	"bytes"
	"context"
	"log"
	"os/exec"
	"time"
)

const (
	pfctlPath       = "/sbin/pfctl"
	pfctlTimeout    = 30 * time.Second
	pfLimitCacheTTL = time.Minute
)

// stateSource reads the pf state table through pfctl(8), which needs root
// (read access to /dev/pf).
type stateSource struct {
	errCount  int
	limit     int
	limitTime time.Time
}

func newStateSource() *stateSource { return &stateSource{} }

func (s *stateSource) probe() bool {
	if _, err := runPfctl("-s", "states"); err != nil {
		log.Printf("conntrack: pfctl state dump failed: %v", err)
		log.Println("conntrack: NAT tracking disabled — ensure pf is loaded and the daemon runs as root")
		return false
	}
	log.Println("conntrack: reading pf state table via pfctl")
	return true
}

func (s *stateSource) close() {}

func (s *stateSource) dump(fn func(Entry)) (count, max int, ok bool) {
	out, err := runPfctl("-s", "states", "-v")
	if err != nil {
		if s.errCount == 0 || s.errCount%60 == 0 {
			log.Printf("conntrack: pfctl -s states: %v", err)
		}
		s.errCount++
		return 0, 0, false
	}
	s.errCount = 0

	entries, count := parsePFStates(bytes.NewReader(out))
	for _, e := range entries {
		fn(e)
	}

	if time.Since(s.limitTime) >= pfLimitCacheTTL {
		if mem, err := runPfctl("-s", "memory"); err == nil {
			s.limit = parsePFStateLimit(bytes.NewReader(mem))
		}
		s.limitTime = time.Now()
	}
	return count, s.limit, true
}

func runPfctl(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pfctlTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, pfctlPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := bytes.TrimSpace(stderr.Bytes()); len(msg) > 0 {
			return nil, &pfctlError{err: err, msg: string(msg)}
		}
		return nil, err
	}
	return out, nil
}

type pfctlError struct {
	err error
	msg string
}

func (e *pfctlError) Error() string { return e.err.Error() + ": " + e.msg }
func (e *pfctlError) Unwrap() error { return e.err }
