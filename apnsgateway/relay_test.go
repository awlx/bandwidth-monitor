package main

import "testing"

// A registered serverURL must resolve only to public addresses — a bandwidth-monitor server can
// never legitimately live at a private/loopback/link-local address anyway, since the relay reaches
// it over the public internet. This is the relay's core SSRF defense: reject targets that could
// only be internal-network or cloud-metadata probing, not a real user's router.
func TestValidateServerURLRejectsNonPublicTargets(t *testing.T) {
	reject := []string{
		"http://127.0.0.1:8080",   // loopback
		"http://localhost:8080",   // loopback
		"http://192.168.1.1:8080", // RFC1918 private
		"http://10.0.0.5:8080",    // RFC1918 private
		"http://169.254.169.254/", // link-local / cloud metadata
		"http://[::1]:8080",       // IPv6 loopback
		"ftp://example.com",       // wrong scheme
		"not a url at all",        // unparseable
		"",                        // empty
	}
	for _, raw := range reject {
		if _, err := validateServerURL(raw); err == nil {
			t.Errorf("validateServerURL(%q) = nil error, want rejection", raw)
		}
	}
}

func TestValidateServerURLAcceptsPublicHTTPTarget(t *testing.T) {
	// A well-known public DNS resolver's IP, reachable and unambiguously not a private/internal
	// address — stands in for "someone's real, internet-exposed bandwidth-monitor server".
	if _, err := validateServerURL("http://8.8.8.8:8080"); err != nil {
		t.Errorf("validateServerURL(public IP) = %v, want accepted", err)
	}
}
