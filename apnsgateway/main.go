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
	"strings"
	"syscall"
	"time"

	"bandwidth-monitor/apns"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	listenAddr := env("LISTEN", ":8443")
	listenProto := strings.ToLower(strings.TrimSpace(env("LISTEN_PROTOCOL", "http")))
	tlsCertFile := env("TLS_CERT_FILE", "")
	tlsKeyFile := env("TLS_KEY_FILE", "")

	if listenProto != "http" && listenProto != "https" {
		log.Fatalf("LISTEN_PROTOCOL: invalid value %q (expected http or https)", listenProto)
	}
	if listenProto == "https" && (tlsCertFile == "" || tlsKeyFile == "") {
		log.Fatal("LISTEN_PROTOCOL=https requires TLS_CERT_FILE and TLS_KEY_FILE")
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

	relay := NewRelay(apnsClient, interval)
	go relay.Run()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/liveactivity/register", relay.HandleRegister)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Printf("apnsgateway: starting on %s (%s), key=%s team=%s bundle=%s push-interval=%s",
		listenAddr, strings.ToUpper(listenProto), keyID, teamID, bundleID, interval)

	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go awaitShutdown(srv)

	serveErr := serve(srv, listenProto, tlsCertFile, tlsKeyFile)
	if serveErr != nil && serveErr != http.ErrServerClosed {
		log.Fatalf("apnsgateway: server failed: %v", serveErr)
	}
	relay.Stop()
}

func serve(srv *http.Server, proto, certFile, keyFile string) error {
	if proto == "https" {
		return srv.ListenAndServeTLS(certFile, keyFile)
	}
	return srv.ListenAndServe()
}

// awaitShutdown blocks until SIGINT/SIGTERM, then gracefully shuts srv down (drains active
// connections, up to 5s).
func awaitShutdown(srv *http.Server) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("\nShutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
}
