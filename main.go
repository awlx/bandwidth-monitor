package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bandwidth-monitor/adguard"
	"bandwidth-monitor/collector"
	"bandwidth-monitor/geoip"
	"bandwidth-monitor/handler"
	"bandwidth-monitor/talkers"
	"bandwidth-monitor/unifi"
)

//go:embed static/*
var staticFiles embed.FS

// env returns the value of the environment variable named by key,
// or fallback if the variable is empty/unset.
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	listenAddr := env("LISTEN", ":8080")
	captureDevice := env("DEVICE", "")
	promiscuous := env("PROMISCUOUS", "true")
	promiscuousBool, _ := strconv.ParseBool(promiscuous)
	geoCountry := env("GEO_COUNTRY", "GeoLite2-Country.mmdb")
	geoASN := env("GEO_ASN", "GeoLite2-ASN.mmdb")
	adguardURL := env("ADGUARD_URL", "")
	adguardUser := env("ADGUARD_USER", "")
	adguardPass := env("ADGUARD_PASS", "")
	unifiURL := env("UNIFI_URL", "")
	unifiUser := env("UNIFI_USER", "")
	unifiPass := env("UNIFI_PASS", "")
	unifiSite := env("UNIFI_SITE", "default")

	geoDB, err := geoip.Open(geoCountry, geoASN)
	if err != nil {
		log.Printf("GeoIP: %v (continuing without geo)", err)
		geoDB = nil
	} else if geoDB.Available() {
		log.Println("GeoIP databases loaded")
		defer geoDB.Close()
	} else {
		log.Println("GeoIP: no MMDB files found (continuing without geo)")
	}

	// Parse VPN_STATUS_FILES: comma-separated "iface=path" pairs
	// e.g. VPN_STATUS_FILES=ffmuc=/run/ffmuc-active,wg0=/run/wg0-active
	vpnStatusFiles := make(map[string]string)
	if raw := os.Getenv("VPN_STATUS_FILES"); raw != "" {
		for _, entry := range strings.Split(raw, ",") {
			parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
			if len(parts) == 2 {
				vpnStatusFiles[parts[0]] = parts[1]
			}
		}
	}

	statsCollector := collector.New(vpnStatusFiles)
	go statsCollector.Run()

	talkerTracker := talkers.New(captureDevice, promiscuousBool, geoDB)
	go talkerTracker.Run()

	var adguardClient *adguard.Client
	if adguardURL != "" {
		adguardClient = adguard.New(adguardURL, adguardUser, adguardPass, 10*time.Second)
		go adguardClient.Run()
		log.Printf("AdGuard Home integration enabled: %s", adguardURL)
	}

	var unifiClient *unifi.Client
	if unifiURL != "" {
		unifiClient = unifi.New(unifiURL, unifiUser, unifiPass, unifiSite, 15*time.Second)
		go unifiClient.Run()
		log.Printf("UniFi controller integration enabled: %s", unifiURL)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/interfaces", handler.InterfaceStats(statsCollector))
	mux.HandleFunc("/api/interfaces/history", handler.InterfaceHistory(statsCollector))
	mux.HandleFunc("/api/talkers/bandwidth", handler.TopTalkersBandwidth(talkerTracker))
	mux.HandleFunc("/api/talkers/volume", handler.TopTalkersVolume(talkerTracker))
	mux.HandleFunc("/api/dns", handler.DNSSummary(adguardClient))
	mux.HandleFunc("/api/wifi", handler.WiFiSummary(unifiClient))
	mux.HandleFunc("/api/summary", handler.MenuBarSummary(statsCollector, talkerTracker, adguardClient, unifiClient))
	mux.HandleFunc("/api/ws", handler.WebSocket(statsCollector, talkerTracker, adguardClient, unifiClient))
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(staticSub)))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")
		statsCollector.Stop()
		talkerTracker.Stop()
		if adguardClient != nil {
			adguardClient.Stop()
		}
		if unifiClient != nil {
			unifiClient.Stop()
		}
		os.Exit(0)
	}()

	log.Printf("Bandwidth Monitor starting on %s", listenAddr)
	log.Printf("Open http://localhost%s in your browser", listenAddr)
	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
