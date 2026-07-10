package resolver

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

func TestDisabledResolverSkipsAndResumesPTRLookups(t *testing.T) {
	var queries atomic.Int32
	server := &mdns.Server{
		PacketConn: mustListenUDP(t),
		Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, request *mdns.Msg) {
			queries.Add(1)
			response := new(mdns.Msg)
			response.SetReply(request)
			response.Answer = append(response.Answer, &mdns.PTR{
				Hdr: mdns.RR_Header{
					Name:   request.Question[0].Name,
					Rrtype: mdns.TypePTR,
					Class:  mdns.ClassINET,
					Ttl:    60,
				},
				Ptr: "cached.example.",
			})
			_ = w.WriteMsg(response)
		}),
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	r := newTestResolver(server.PacketConn.LocalAddr().String())
	r.SetEnabled(false)
	if got := r.LookupAddrAsync("192.0.2.10"); got != "192.0.2.10" {
		t.Fatalf("disabled resolver returned %q", got)
	}
	time.Sleep(20 * time.Millisecond)
	if got := queries.Load(); got != 0 {
		t.Fatalf("disabled resolver made %d PTR queries", got)
	}
	r.mu.RLock()
	cacheEntries := len(r.cache)
	r.mu.RUnlock()
	if cacheEntries != 0 {
		t.Fatalf("disabled resolver populated %d cache entries", cacheEntries)
	}

	r.SetEnabled(true)
	if got := r.LookupAddrAsync("192.0.2.10"); got != "192.0.2.10" {
		t.Fatalf("initial enabled lookup returned %q", got)
	}
	waitFor(t, func() bool { return queries.Load() == 1 })
	waitFor(t, func() bool { return r.LookupAddrAsync("192.0.2.10") == "cached.example" })

	r.SetEnabled(false)
	if got := r.LookupAddrAsync("192.0.2.10"); got != "192.0.2.10" {
		t.Fatalf("disabled resolver exposed cached name %q", got)
	}
	r.SetEnabled(true)
	if got := r.LookupAddrAsync("192.0.2.10"); got != "cached.example" {
		t.Fatalf("re-enabled resolver lost cached name: %q", got)
	}
}

func TestResolverEnableDisableIsConcurrentSafe(t *testing.T) {
	r := newTestResolver("127.0.0.1:1")
	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 500; i++ {
				r.SetEnabled(i%2 == 0)
				_ = r.LookupAddrAsync("not-an-ip")
				_ = r.Enabled()
			}
		}()
	}
	workers.Wait()
}

func newTestResolver(server string) *Resolver {
	return &Resolver{
		cache:   make(map[string]cacheEntry),
		enabled: true,
		sem:     make(chan struct{}, 1),
		negTTL:  time.Minute,
		minTTL:  time.Second,
		maxTTL:  time.Minute,
		timeout: time.Second,
		server:  server,
	}
}

func mustListenUDP(t *testing.T) net.PacketConn {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met")
		}
		time.Sleep(time.Millisecond)
	}
}
