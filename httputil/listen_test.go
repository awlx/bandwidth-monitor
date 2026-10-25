package httputil

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseListenAddrs(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"wildcard port only", ":8080", []string{":8080"}},
		{"single host port", "192.0.2.9:443", []string{"192.0.2.9:443"}},
		{"v4 and v6 pair", "192.0.2.9:443,[2001:db8::9]:443", []string{"192.0.2.9:443", "[2001:db8::9]:443"}},
		{"surrounding spaces", " 192.0.2.9:443 , [2001:db8::9]:443 ", []string{"192.0.2.9:443", "[2001:db8::9]:443"}},
		{"empty entries ignored", ":8080,,", []string{":8080"}},
		{"duplicates collapsed", ":8080,:8080", []string{":8080"}},
		{"hostname", "localhost:8080", []string{"localhost:8080"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseListenAddrs(tt.raw)
			if err != nil {
				t.Fatalf("ParseListenAddrs(%q) returned error: %v", tt.raw, err)
			}
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("ParseListenAddrs(%q) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseListenAddrsErrors(t *testing.T) {
	for _, raw := range []string{"", "   ", ",", ":8080,nonsense", "192.0.2.9"} {
		if got, err := ParseListenAddrs(raw); err == nil {
			t.Errorf("ParseListenAddrs(%q) = %v, want error", raw, got)
		}
	}
}

// An unbracketed IPv6 address with a port is the most likely mistake, so the
// error must name the bracketed form the user actually needs.
func TestParseListenAddrsUnbracketedIPv6Hint(t *testing.T) {
	_, err := ParseListenAddrs("[2001:db8::9]:443,2001:db8:1::9:443")
	if err == nil {
		t.Fatal("expected an error for an unbracketed IPv6 address")
	}
	if !strings.Contains(err.Error(), "[2001:db8:1::9]:443") {
		t.Errorf("error %q does not suggest the bracketed form", err)
	}
}

// freePort returns a port that was free on host a moment ago.
func freePort(t *testing.T, host string) string {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", host, err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", ln.Addr(), err)
	}
	return port
}

func TestServersServeEveryAddress(t *testing.T) {
	addrs := []string{
		net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1")),
		net.JoinHostPort("::1", freePort(t, "::1")),
	}

	srv := NewServers(addrs, okServer)
	if got := strings.Join(srv.Addrs(), "|"); got != strings.Join(addrs, "|") {
		t.Errorf("Addrs() = %v, want %v", srv.Addrs(), addrs)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe("", "") }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		if err := <-errCh; err != nil {
			t.Errorf("ListenAndServe returned %v, want nil after Shutdown", err)
		}
	})

	client := &http.Client{Timeout: 5 * time.Second}
	for _, addr := range addrs {
		if err := waitForServer(client, addr); err != nil {
			t.Fatalf("GET http://%s: %v", addr, err)
		}
	}
}

func okServer(addr string) *http.Server {
	return &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})}
}

func waitForServer(client *http.Client, addr string) error {
	return waitForServerScheme(client, "http", addr)
}

// waitForServerScheme polls addr until it answers with the expected body,
// since ListenAndServe starts accepting a moment after it is called.
func waitForServerScheme(client *http.Client, scheme, addr string) error {
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(scheme + "://" + addr + "/")
		if err != nil {
			lastErr = err
			time.Sleep(10 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "ok" {
			return fmt.Errorf("body = %q, want %q", body, "ok")
		}
		return nil
	}
	return lastErr
}

// A bad address must fail at startup and leave nothing listening, rather than
// serving on the addresses that did bind.
func TestServersListenFailureReleasesGoodAddress(t *testing.T) {
	good := net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1"))
	addrs := []string{good, "192.0.2.111:9"} // TEST-NET-1: not a local address

	srv := NewServers(addrs, okServer)
	err := srv.ListenAndServe("", "")
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		t.Fatal("ListenAndServe succeeded, want a bind error")
	}
	if !strings.Contains(err.Error(), "192.0.2.111:9") {
		t.Errorf("error %q does not name the failing address", err)
	}

	ln, err := net.Listen("tcp", good)
	if err != nil {
		t.Fatalf("%s still bound after a failed startup: %v", good, err)
	}
	ln.Close()
}

func TestServersShutdownIsIdempotent(t *testing.T) {
	srv := NewServers([]string{net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1"))}, okServer)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe("", "") }()

	client := &http.Client{Timeout: 5 * time.Second}
	if err := waitForServer(client, srv.Addrs()[0]); err != nil {
		t.Fatalf("server never came up: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("first Shutdown: %v", err)
	}
	if err := srv.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("ListenAndServe returned %v", err)
	}
}

// Each address gets its own http.Server so that concurrent TLS setup on the
// listeners cannot race on a shared TLSConfig; run under -race to check that.
func TestServersServeTLSOnEveryAddress(t *testing.T) {
	certFile, keyFile := writeSelfSignedCert(t)
	addrs := []string{
		net.JoinHostPort("127.0.0.1", freePort(t, "127.0.0.1")),
		net.JoinHostPort("::1", freePort(t, "::1")),
	}

	srv := NewServers(addrs, okServer)
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe(certFile, keyFile) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
		if err := <-errCh; err != nil {
			t.Errorf("ListenAndServe returned %v, want nil after Shutdown", err)
		}
	})

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
	}
	for _, addr := range addrs {
		if err := waitForServerScheme(client, "https", addr); err != nil {
			t.Fatalf("GET https://%s: %v", addr, err)
		}
	}
}

func writeSelfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	writePEM(t, certFile, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	writePEM(t, keyFile, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certFile, keyFile
}

func writePEM(t *testing.T, path string, block *pem.Block) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, block); err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
}
