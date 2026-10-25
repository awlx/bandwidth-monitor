// Command apnsgateway is a standalone Live Activity push relay for Bandwidth Monitor. See relay.go
// for the registration/polling/SSRF-hardening design.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bandwidth-monitor/apns"
	"bandwidth-monitor/httputil"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	listenAddrs, err := httputil.ParseListenAddrs(env("LISTEN", ":8443"))
	if err != nil {
		log.Fatalf("LISTEN: %v", err)
	}
	tlsCertFile := env("TLS_CERT_FILE", "")
	tlsKeyFile := env("TLS_KEY_FILE", "")

	// apnsgateway always serves HTTPS: the iOS client uses App Transport
	// Security defaults and will refuse to reach a plain-HTTP endpoint, so
	// a misconfigured cert should fail loudly at startup rather than come
	// up on HTTP and silently reject every registration.
	if tlsCertFile == "" || tlsKeyFile == "" {
		log.Fatal("apnsgateway requires TLS: set TLS_CERT_FILE and TLS_KEY_FILE to your certificate and private key paths")
	}

	keyID := env("APNS_KEY_ID", "")
	teamID := env("APNS_TEAM_ID", "")
	bundleID := env("APNS_BUNDLE_ID", "")
	keyFile := env("APNS_KEY_FILE", "")
	if keyFile == "" || keyID == "" || teamID == "" || bundleID == "" {
		log.Fatal("APNS_KEY_FILE, APNS_KEY_ID, APNS_TEAM_ID, and APNS_BUNDLE_ID are all required")
	}
	apnsClient, err := apns.NewClient(apns.Config{KeyFile: keyFile, KeyID: keyID, TeamID: teamID, BundleID: bundleID})
	if err != nil {
		log.Fatalf("apns: %v", err)
	}

	interval, err := time.ParseDuration(env("APNS_PUSH_INTERVAL", "10s"))
	if err != nil {
		log.Fatalf("APNS_PUSH_INTERVAL: %v", err)
	}

	maxResponseBytes, err := strconv.ParseInt(env("APNS_MAX_RESPONSE_BYTES", strconv.Itoa(DefaultMaxResponseBytes)), 10, 64)
	if err != nil || maxResponseBytes <= 0 {
		log.Fatalf("APNS_MAX_RESPONSE_BYTES: invalid value %q (expected a positive byte count)", env("APNS_MAX_RESPONSE_BYTES", ""))
	}

	relay := NewRelay(apnsClient, interval, maxResponseBytes)
	go relay.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/liveactivity/register", relay.HandleRegister)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Printf("apnsgateway: starting on %s (HTTPS) tls-cert=%s tls-key=%s, apns-key-id=%s team=%s bundle=%s push-interval=%s max-response-bytes=%d",
		strings.Join(listenAddrs, ", "), tlsCertFile, tlsKeyFile, keyID, teamID, bundleID, interval, maxResponseBytes)

	srv := httputil.NewServers(listenAddrs, func(addr string) *http.Server {
		return &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
	})

	go awaitShutdown(srv)

	if serveErr := srv.ListenAndServe(tlsCertFile, tlsKeyFile); serveErr != nil {
		log.Fatalf("apnsgateway: server failed: %v", serveErr)
	}
	relay.Stop()
}

// awaitShutdown blocks until SIGINT/SIGTERM, then gracefully shuts srv down (drains active
// connections, up to 5s).
func awaitShutdown(srv *httputil.Servers) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
