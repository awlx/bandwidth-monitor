package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bandwidth-monitor/adguard"
	"bandwidth-monitor/collector"
	"bandwidth-monitor/conntrack"
	"bandwidth-monitor/dns"
	"bandwidth-monitor/geoip"
	"bandwidth-monitor/handler"
	"bandwidth-monitor/latency"
	"bandwidth-monitor/nextdns"
	"bandwidth-monitor/omada"
	"bandwidth-monitor/pihole"
	"bandwidth-monitor/resolver"
	"bandwidth-monitor/speedtest"
	"bandwidth-monitor/talkers"
	"bandwidth-monitor/topology"
	"bandwidth-monitor/unifi"
	"bandwidth-monitor/version"
	"bandwidth-monitor/wifi"
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
	listenProto := strings.ToLower(strings.TrimSpace(env("LISTEN_PROTOCOL", "http")))
	tlsCertFile := env("TLS_CERT_FILE", "")
	tlsKeyFile := env("TLS_KEY_FILE", "")
	promiscuous := env("PROMISCUOUS", "true")
	promiscuousBool, _ := strconv.ParseBool(promiscuous)

	if listenProto != "http" && listenProto != "https" {
		log.Fatalf("LISTEN_PROTOCOL: invalid value %q (expected http or https)", listenProto)
	}
	if listenProto == "https" {
		if tlsCertFile == "" || tlsKeyFile == "" {
			log.Fatal("LISTEN_PROTOCOL=https requires TLS_CERT_FILE and TLS_KEY_FILE")
		}
	}

	// Parse LOCAL_NETS: comma-separated CIDRs for SPAN port direction detection
	// e.g. LOCAL_NETS=192.0.2.0/24,2001:db8::/48
	// If not set, auto-discovers from local interface addresses.
	var localNets []*net.IPNet
	if raw := os.Getenv("LOCAL_NETS"); raw != "" {
		for _, cidr := range strings.Split(raw, ",") {
			cidr = strings.TrimSpace(cidr)
			if cidr == "" {
				continue
			}
			_, ipnet, err := net.ParseCIDR(cidr)
			if err != nil {
				log.Printf("LOCAL_NETS: invalid CIDR %q: %v", cidr, err)
				continue
			}
			localNets = append(localNets, ipnet)
		}
		log.Printf("LOCAL_NETS: %d network(s) from configuration", len(localNets))
	} else {
		// Auto-discover from local interfaces
		ifaces, err := net.Interfaces()
		if err == nil {
			for _, iface := range ifaces {
				if iface.Flags&net.FlagLoopback != 0 {
					continue
				}
				addrs, err := iface.Addrs()
				if err != nil {
					continue
				}
				for _, addr := range addrs {
					ipnet, ok := addr.(*net.IPNet)
					if !ok {
						continue
					}
					// Skip link-local
					if ipnet.IP.IsLinkLocalUnicast() || ipnet.IP.IsLinkLocalMulticast() {
						continue
					}
					localNets = append(localNets, ipnet)
				}
			}
		}
		if len(localNets) > 0 {
			log.Printf("LOCAL_NETS: auto-discovered %d network(s) from interfaces", len(localNets))
			for _, n := range localNets {
				log.Printf("  %s", n.String())
			}
		}
	}

	// GeoIP: prefer City DB (has country + city + coordinates), fall back to Country DB.
	// Check env vars first, then auto-detect from common filenames on disk.
	// Always verify the file exists — stale env vars may point to missing files.
	geoCity := env("GEO_CITY", env("GEO_COUNTRY", ""))
	if geoCity != "" {
		if _, err := os.Stat(geoCity); err != nil {
			log.Printf("GeoIP: configured path %q not found, auto-detecting", geoCity)
			geoCity = ""
		}
	}
	if geoCity == "" {
		// Auto-detect: try City first, fall back to Country
		for _, candidate := range []string{"GeoLite2-City.mmdb", "GeoLite2-Country.mmdb"} {
			if _, err := os.Stat(candidate); err == nil {
				geoCity = candidate
				break
			}
		}
	}
	geoASN := env("GEO_ASN", "GeoLite2-ASN.mmdb")
	adguardURL := env("ADGUARD_URL", "")
	adguardUser := env("ADGUARD_USER", "")
	adguardPass := env("ADGUARD_PASS", "")
	nextdnsProfile := env("NEXTDNS_PROFILE", "")
	nextdnsAPIKey := env("NEXTDNS_API_KEY", "")
	piholeURL := env("PIHOLE_URL", "")
	piholePass := env("PIHOLE_PASSWORD", "")
	unifiURL := env("UNIFI_URL", "")
	unifiUser := env("UNIFI_USER", "")
	unifiPass := env("UNIFI_PASS", "")
	unifiSite := env("UNIFI_SITE", "default")
	omadaURL := env("OMADA_URL", "")
	omadaUser := env("OMADA_USER", "")
	omadaPass := env("OMADA_PASS", "")
	omadaSite := env("OMADA_SITE", "Default")

	geoDB, err := geoip.Open(geoCity, geoASN)
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
	// e.g. VPN_STATUS_FILES=myvpn=/run/myvpn-active,wg0=/run/wg0-active
	vpnStatusFiles := make(map[string]string)
	if raw := os.Getenv("VPN_STATUS_FILES"); raw != "" {
		for _, entry := range strings.Split(raw, ",") {
			parts := strings.SplitN(strings.TrimSpace(entry), "=", 2)
			if len(parts) == 2 {
				vpnStatusFiles[parts[0]] = parts[1]
			}
		}
	}

	// Parse INTERFACES: comma-separated list of interface names to display.
	// If not set, all interfaces are shown.
	var allowedIfaces []string
	if raw := os.Getenv("INTERFACES"); raw != "" {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				allowedIfaces = append(allowedIfaces, name)
			}
		}
		log.Printf("INTERFACES: showing %d interface(s): %s", len(allowedIfaces), strings.Join(allowedIfaces, ", "))
	}

	// Parse WAN_INTERFACE: comma-separated list of interface names to treat as WAN.
	// If not set, WAN is auto-detected (public IP, PPP, or default route).
	var wanIfaces []string
	if raw := os.Getenv("WAN_INTERFACE"); raw != "" {
		for _, name := range strings.Split(raw, ",") {
			name = strings.TrimSpace(name)
			if name != "" {
				wanIfaces = append(wanIfaces, name)
			}
		}
		log.Printf("WAN_INTERFACE: %s", strings.Join(wanIfaces, ", "))
	}

	statsCollector := collector.New(vpnStatusFiles, allowedIfaces, wanIfaces)

	// Shared reverse-DNS resolver — used by talkers, conntrack, and debug.
	dnsResolver := resolver.New()

	// SPAN/mirror port mode: override RX/TX direction on a specific interface
	// using pcap-based packet inspection against LOCAL_NETS.
	spanDevice := env("SPAN_DEVICE", "")
	if spanDevice != "" && len(localNets) > 0 {
		statsCollector.EnableSPAN(spanDevice, promiscuousBool, localNets)
		log.Printf("span: enabled on %s (%d local nets)", spanDevice, len(localNets))
	} else if spanDevice != "" && len(localNets) == 0 {
		log.Printf("span: SPAN_DEVICE=%s set but LOCAL_NETS is empty — disabled", spanDevice)
	}

	go statsCollector.Run()

	talkerTracker := talkers.New(allowedIfaces, promiscuousBool, localNets, geoDB, dnsResolver)
	go talkerTracker.Run()

	// DNS provider: AdGuard Home, NextDNS, or Pi-hole (mutually exclusive; first configured wins)
	var dnsProvider dns.Provider
	if adguardURL != "" {
		ac := adguard.New(adguardURL, adguardUser, adguardPass, 10*time.Second)
		go ac.Run()
		dnsProvider = ac
		log.Printf("DNS integration: AdGuard Home (%s)", adguardURL)
	} else if nextdnsProfile != "" && nextdnsAPIKey != "" {
		nc := nextdns.New(nextdnsProfile, nextdnsAPIKey, 30*time.Second)
		go nc.Run()
		dnsProvider = nc
		log.Printf("DNS integration: NextDNS (profile %s)", nextdnsProfile)
	} else if piholeURL != "" {
		pc := pihole.New(piholeURL, piholePass, 10*time.Second)
		go pc.Run()
		dnsProvider = pc
		log.Printf("DNS integration: Pi-hole (%s)", piholeURL)
	}

	// WiFi provider: UniFi or Omada (mutually exclusive; first configured wins)
	var wifiProvider wifi.Provider
	if unifiURL != "" {
		uc := unifi.New(unifiURL, unifiUser, unifiPass, unifiSite, 15*time.Second)
		go uc.Run()
		wifiProvider = uc
		log.Printf("WiFi integration: UniFi (%s)", unifiURL)
	} else if omadaURL != "" {
		oc := omada.New(omadaURL, omadaUser, omadaPass, omadaSite, 15*time.Second)
		go oc.Run()
		wifiProvider = oc
		log.Printf("WiFi integration: Omada (%s)", omadaURL)
	}

	conntrackTracker := conntrack.New(localNets, geoDB, dnsResolver)
	go conntrackTracker.Run()
	log.Println("Conntrack (NAT) tracking enabled")

	speedtestServer := env("SPEEDTEST_SERVER", "https://speed.ffmuc.net")
	speedTester := speedtest.New(speedtestServer)
	log.Printf("Speed test server: %s", speedtestServer)

	// Latency monitor: continuous ICMP + HTTPS probes.
	// LATENCY_TARGETS: comma-separated hostnames/IPs. Defaults to FFMUC + GitHub.
	var latencyTargets []string
	if raw := os.Getenv("LATENCY_TARGETS"); raw != "" {
		for _, t := range strings.Split(raw, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				latencyTargets = append(latencyTargets, t)
			}
		}
	}
	latencyMonitor := latency.New(latencyTargets)
	go latencyMonitor.Run()

	topoScanner := topology.New(dnsResolver, wifiProvider, localNets, 30*time.Second)
	topoScanner.SetWANInterfacesFunc(func() []string {
		var names []string
		for _, iface := range statsCollector.GetAll() {
			if iface.WAN {
				names = append(names, iface.Name)
			}
		}
		return names
	})
	go topoScanner.Run()
	log.Println("Network topology scanner enabled")

	mux := http.NewServeMux()
	mux.HandleFunc("/api/interfaces", handler.InterfaceStats(statsCollector))
	mux.HandleFunc("/api/interfaces/history", handler.InterfaceHistory(statsCollector))
	mux.HandleFunc("/api/talkers/bandwidth", handler.TopTalkersBandwidth(talkerTracker))
	mux.HandleFunc("/api/talkers/volume", handler.TopTalkersVolume(talkerTracker))
	mux.HandleFunc("/api/dns", handler.DNSSummary(dnsProvider, dnsResolver))
	mux.HandleFunc("/api/wifi", handler.WiFiSummary(wifiProvider))
	mux.HandleFunc("/api/conntrack", handler.ConntrackSummary(conntrackTracker))
	mux.HandleFunc("/api/host", handler.HostDetail(talkerTracker, conntrackTracker, geoDB))
	mux.HandleFunc("/api/latency", handler.LatencyStatus(latencyMonitor))
	mux.HandleFunc("/api/speedtest/run", handler.SpeedTestRun(speedTester))
	mux.HandleFunc("/api/speedtest/results", handler.SpeedTestResults(speedTester))
	mux.HandleFunc("/api/debug/traceroute", handler.DebugTraceroute(dnsResolver))
	mux.HandleFunc("/api/debug/dns", handler.DebugDNS())
	mux.HandleFunc("/api/debug/mtu", handler.DebugMTU())
	mux.HandleFunc("/api/summary", handler.MenuBarSummary(statsCollector, talkerTracker, dnsProvider, wifiProvider, conntrackTracker))
	mux.HandleFunc("/api/topology", handler.TopologySummary(topoScanner))
	mux.HandleFunc("/api/events", handler.SSE(statsCollector, talkerTracker, dnsProvider, wifiProvider, conntrackTracker, latencyMonitor, topoScanner, dnsResolver, geoDB))
	staticSub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("Failed to create sub filesystem: %v", err)
	}

	// Compute content hashes for cache-busting query strings.
	// embed.FS has a zero modtime so http.FileServer omits Last-Modified;
	// Safari caches the response with heuristic expiration and never
	// revalidates — even on Cmd+R.  Injecting ?v=<hash> into the HTML
	// forces the browser to fetch fresh assets after every build.
	assetVersion := computeAssetVersions(staticFiles)
	indexHTML := buildIndexHTML(staticFiles, assetVersion)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			// Serve other static files with ETag-based caching.
			w.Header().Set("Cache-Control", "no-cache")
			http.FileServer(http.FS(staticSub)).ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(indexHTML)
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("server: bandwidth-monitor %s starting on %s (%s)", version.String(), listenAddr, strings.ToUpper(listenProto))
	if strings.HasPrefix(listenAddr, ":") {
		log.Printf("server: open %s://localhost%s in your browser", listenProto, listenAddr)
	} else {
		log.Printf("server: open %s://%s in your browser", listenProto, listenAddr)
	}
	if listenProto == "https" {
		log.Printf("server: TLS enabled cert=%s key=%s", tlsCertFile, tlsKeyFile)
	}
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           withSignature(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		<-sigCh
		fmt.Println("\nShutting down...")

		// Gracefully shut down the HTTP server (drains active connections).
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	serveErr := error(nil)
	if listenProto == "https" {
		serveErr = srv.ListenAndServeTLS(tlsCertFile, tlsKeyFile)
	} else {
		serveErr = srv.ListenAndServe()
	}
	if serveErr != nil && serveErr != http.ErrServerClosed {
		log.Fatalf("Server failed: %v", serveErr)
	}

	// Clean up all subsystems — defers (e.g. geoDB.Close) will also run.
	statsCollector.Stop()
	talkerTracker.Stop()
	if dnsProvider != nil {
		dnsProvider.Stop()
	}
	if wifiProvider != nil {
		wifiProvider.Stop()
	}
	conntrackTracker.Stop()
	topoScanner.Stop()
	latencyMonitor.Stop()
	dnsResolver.Stop()
}

// computeAssetVersions hashes key embedded files to produce short
// cache-busting version strings.  The returned map is keyed by
// filename (e.g. "app.js") → 8-char hex hash.
func computeAssetVersions(embedded embed.FS) map[string]string {
	versions := make(map[string]string)
	for _, name := range []string{"static/js/core.js", "static/style.css"} {
		data, err := embedded.ReadFile(name)
		if err != nil {
			continue
		}
		h := sha256.Sum256(data)
		// Use the path relative to static/ as key (e.g. "js/core.js", "style.css")
		relPath := strings.TrimPrefix(name, "static/")
		versions[relPath] = hex.EncodeToString(h[:4])
	}
	return versions
}

// buildIndexHTML reads the embedded index.html and injects ?v=<hash>
// into script src and link href attributes for cache-busted assets.
func buildIndexHTML(embedded embed.FS, versions map[string]string) []byte {
	data, err := embedded.ReadFile("static/index.html")
	if err != nil {
		log.Fatalf("embedded index.html: %v", err)
	}
	html := string(data)
	for name, ver := range versions {
		// Replace href="style.css" → href="style.css?v=abcd1234"
		// Replace src="app.js"     → src="app.js?v=abcd1234"
		html = strings.ReplaceAll(html, `"`+name+`"`, `"`+name+"?v="+ver+`"`)
	}
	// Inject the build version into the header.
	html = strings.Replace(html, "Bandwidth Monitor<span>v1.0</span>", "Bandwidth Monitor<span>"+version.String()+"</span>", 1)
	return []byte(html)
}

// withSignature wraps an http.Handler to inject a X-Bandwidth-Monitor header
// on every response, allowing clients to verify they're talking to the right service.
func withSignature(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Bandwidth-Monitor", version.String())
		h.ServeHTTP(w, r)
	})
}
