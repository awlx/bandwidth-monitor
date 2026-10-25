package httputil

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ParseListenAddrs splits a comma-separated LISTEN value ("host:port") into
// individual bind addresses, so a daemon can serve on a specific v4 and v6
// address instead of having to fall back to the "*" wildcard:
//
//	LISTEN=192.0.2.9:443,[2001:db8::9]:443
//
// IPv6 literals must be bracketed, exactly as elsewhere in Go's net package —
// an unbracketed address is itself ambiguous with a host:port pair, so it is
// rejected with a hint rather than guessed at. Duplicates are collapsed and
// empty entries ignored; the returned slice always has at least one address.
func ParseListenAddrs(raw string) ([]string, error) {
	var addrs []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(part); err != nil {
			return nil, fmt.Errorf("invalid bind address %q: %w%s", part, err, bracketHint(part))
		}
		if seen[part] {
			continue
		}
		seen[part] = true
		addrs = append(addrs, part)
	}
	if len(addrs) == 0 {
		return nil, errors.New("no bind address given")
	}
	return addrs, nil
}

// bracketHint returns " (did you mean [addr]:port?)" when addr looks like an
// unbracketed IPv6 address with a port appended — by far the most likely way
// to get an address wrong here.
func bracketHint(addr string) string {
	i := strings.LastIndex(addr, ":")
	if i < 0 || strings.Contains(addr, "[") {
		return ""
	}
	host, port := addr[:i], addr[i+1:]
	if net.ParseIP(host) == nil {
		return ""
	}
	if _, err := strconv.Atoi(port); err != nil {
		return ""
	}
	return fmt.Sprintf(" (did you mean [%s]:%s?)", host, port)
}

// Servers is a group of HTTP servers, one per bind address, sharing a handler
// and started and stopped as a unit. Each address gets its own *http.Server
// rather than one server serving several listeners: a server mutates its
// TLSConfig when it configures HTTP/2, which is not safe to do while sibling
// listeners are already handshaking against that same config.
type Servers struct {
	servers []*http.Server
}

// NewServers builds one server per address in addrs. build is called once per
// address and must return a fresh *http.Server; its Addr field is set from
// addr, so build can ignore it and only set Handler and timeouts.
func NewServers(addrs []string, build func(addr string) *http.Server) *Servers {
	s := &Servers{servers: make([]*http.Server, 0, len(addrs))}
	for _, addr := range addrs {
		srv := build(addr)
		srv.Addr = addr
		s.servers = append(s.servers, srv)
	}
	return s
}

// Addrs returns the bind addresses in configuration order.
func (s *Servers) Addrs() []string {
	addrs := make([]string, 0, len(s.servers))
	for _, srv := range s.servers {
		addrs = append(addrs, srv.Addr)
	}
	return addrs
}

// ListenAndServe binds every address and serves until all servers stop,
// returning the first error that is not http.ErrServerClosed. When certFile
// is non-empty the servers speak HTTPS using certFile and keyFile.
//
// Every address is bound before any of them starts serving, so a busy port or
// an address that does not exist on this host fails at startup rather than
// leaving the daemon half-listening.
func (s *Servers) ListenAndServe(certFile, keyFile string) error {
	listeners := make([]net.Listener, 0, len(s.servers))
	for _, srv := range s.servers {
		ln, err := net.Listen("tcp", srv.Addr)
		if err != nil {
			for _, opened := range listeners {
				opened.Close()
			}
			return fmt.Errorf("listen on %s: %w", srv.Addr, err)
		}
		listeners = append(listeners, ln)
	}

	errCh := make(chan error, len(s.servers))
	for i, srv := range s.servers {
		go func(srv *http.Server, ln net.Listener) {
			if certFile != "" {
				errCh <- srv.ServeTLS(ln, certFile, keyFile)
				return
			}
			errCh <- srv.Serve(ln)
		}(srv, listeners[i])
	}

	var firstErr error
	for range s.servers {
		err := <-errCh
		if err == nil || errors.Is(err, http.ErrServerClosed) || firstErr != nil {
			continue
		}
		firstErr = err
		// One bind died on its own; stop the rest so the caller reports a
		// single clear failure instead of continuing to serve on a subset of
		// the configured addresses.
		s.Close()
	}
	return firstErr
}

// Shutdown gracefully shuts every server down, returning the first error.
func (s *Servers) Shutdown(ctx context.Context) error {
	var firstErr error
	for _, srv := range s.servers {
		if err := srv.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Close immediately closes every server and its active connections.
func (s *Servers) Close() error {
	var firstErr error
	for _, srv := range s.servers {
		if err := srv.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
