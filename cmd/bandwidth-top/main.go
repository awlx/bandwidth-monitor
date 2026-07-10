//go:build linux

package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"bandwidth-monitor/bandwidthtop"
	"bandwidth-monitor/resolver"
	"bandwidth-monitor/talkers"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "bandwidth-top:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("bandwidth-top", flag.ContinueOnError)
	fs.SetOutput(stderr)
	ifaceName := fs.String("interface", "", "capture interface (default: lowest-metric default route)")
	var localNetworkFlags bandwidthtop.LocalNetworkFlags
	fs.Var(&localNetworkFlags, "local-network", "local CIDR override (repeatable; replaces interface prefixes)")
	rows := fs.Int("rows", 20, "maximum rows")
	refresh := fs.Duration("refresh", time.Second, "refresh interval")
	snapshot := fs.Bool("snapshot", false, "print one plain snapshot and exit")
	asnPath := fs.String("asn-mmdb", discover("GeoLite2-ASN.mmdb"), "ASN MMDB path")
	cityPath := fs.String("city-mmdb", discoverCity(), "city/country MMDB path")
	server := fs.String("server", "", "bandwidth-monitor base URL for enrichment")
	noServerDiscovery := fs.Bool("no-server-discovery", false, "disable one-time default-gateway monitor discovery")
	publicURL := fs.String("public-url", bandwidthtop.DefaultPublicURL, "public enrichment API base URL")
	noPublic := fs.Bool("no-public", false, "disable public enrichment fallback")
	noResolve := false
	fs.BoolVar(&noResolve, "no-resolve", false, "disable reverse DNS and show remote IPs")
	fs.BoolVar(&noResolve, "n", false, "disable reverse DNS and show remote IPs (shorthand)")
	width := fs.Int("width", 0, "output width in columns (default: terminal width)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: bandwidth-top [options]")
		fmt.Fprintln(stderr, "Live AF_PACKET traffic viewer; requires root or CAP_NET_RAW.")
		fmt.Fprintln(stderr, "Local databases and monitor readiness are checked once at startup.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rows <= 0 || *refresh <= 0 {
		return fmt.Errorf("rows and refresh must be positive")
	}

	selected, err := bandwidthtop.SelectCaptureInterface(*ifaceName)
	if err != nil {
		return err
	}
	iface := selected.Interface
	localNetworks := bandwidthtop.EffectiveLocalNetworks(selected.LocalNets, localNetworkFlags.Networks())
	monitorURL, monitorDiscovery := bandwidthtop.MonitorServerURL(
		*server, *noServerDiscovery, selected.Gateway, iface.Name)
	enricher, err := bandwidthtop.NewEnricherWithDatabases(bandwidthtop.Config{
		ServerURL: monitorURL, PublicURL: *publicURL, DisablePublic: *noPublic,
		MonitorDiscovery: monitorDiscovery, DisableMonitorDiscovery: *noServerDiscovery,
	}, *cityPath, *asnPath)
	if err != nil {
		return err
	}
	defer enricher.Close()
	for _, warning := range enricher.StartupWarnings() {
		fmt.Fprintln(stderr, "bandwidth-top:", warning)
	}
	liveTerminal := supportsLiveTerminal(stdout, *snapshot, os.Getenv("TERM"))
	singleFrame := *snapshot || !liveTerminal
	if !*snapshot && !liveTerminal {
		fmt.Fprintln(stderr, "bandwidth-top: non-interactive terminal; rendering one plain snapshot")
	}
	terminal := newTerminalSession(stdout, liveTerminal, func() terminalDimensions {
		return terminalSize(stdout)
	})
	dns := resolverUnlessDisabled(noResolve, resolver.New)
	if dns != nil {
		defer dns.Stop()
	}
	tracker := talkers.NewDirect(iface.Name, false, localNetworks, nil, dns)
	log.SetOutput(io.Discard)
	go tracker.Run()
	defer tracker.Stop()

	delay := *refresh
	if singleFrame && delay < 1200*time.Millisecond {
		delay = 1200 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	resizes := make(chan os.Signal, 1)
	signal.Notify(resizes, syscall.SIGWINCH)
	defer signal.Stop(resizes)
	select {
	case <-signals:
		return nil
	case err := <-tracker.Errors():
		return fmt.Errorf("capture %s failed: %w (run as root or grant CAP_NET_RAW)", iface.Name, err)
	case <-timer.C:
	}

	return terminal.withScreen(func() error {
		for {
			stats, rateTotals := tracker.DirectBandwidthSnapshot(*rows)
			viewRows := make([]bandwidthtop.Row, 0, len(stats))
			for _, stat := range stats {
				viewRows = append(viewRows, bandwidthtop.Row{
					LocalIP:   stat.LocalIP,
					NoResolve: noResolve,
					Stat: bandwidthtop.Stat{
						IP: stat.IP, Hostname: stat.Hostname, Packets: stat.Packets,
						Rx: bandwidthtop.RateWindows{
							Two: stat.RxRate, Ten: stat.RxRate10, Forty: stat.RxRate40,
						},
						Tx: bandwidthtop.RateWindows{
							Two: stat.TxRate, Ten: stat.TxRate10, Forty: stat.TxRate40,
						},
					},
					Info: enricher.Lookup(stat.IP),
				})
			}
			totals := bandwidthtop.Totals{
				Rx: bandwidthtop.RateWindows{
					Two: rateTotals.RxRate, Ten: rateTotals.RxRate10, Forty: rateTotals.RxRate40,
				},
				Tx: bandwidthtop.RateWindows{
					Two: rateTotals.TxRate, Ten: rateTotals.TxRate10, Forty: rateTotals.TxRate40,
				},
			}
			if singleFrame {
				enricher.Wait()
				for i := range viewRows {
					viewRows[i].Info = enricher.Lookup(viewRows[i].Stat.IP)
				}
			}
			size := terminal.dimensions(*width)
			title := fmt.Sprintf("bandwidth-top  interface=%s  refresh=%s  rates=bit/s",
				iface.Name, refresh.String())
			status := enricher.SourceStatusLines(size.width)
			if lookupStatus := lookupErrorStatus(viewRows); lookupStatus != "" {
				status = append(status, lookupStatus)
			}
			frame := composeFrame(title, status, viewRows, totals, *rows, size, liveTerminal)
			if err := terminal.draw(frame); err != nil {
				return err
			}
			if singleFrame {
				return nil
			}
			timer.Reset(*refresh)
			select {
			case <-signals:
				return nil
			case err := <-tracker.Errors():
				return fmt.Errorf("capture %s failed: %w (run as root or grant CAP_NET_RAW)", iface.Name, err)
			case <-resizes:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
			case <-timer.C:
			}
		}
	})
}

func resolverUnlessDisabled(disabled bool, create func() *resolver.Resolver) *resolver.Resolver {
	if disabled {
		return nil
	}
	return create()
}

func lookupErrorStatus(rows []bandwidthtop.Row) string {
	count := 0
	for _, row := range rows {
		if row.Info.Err != "" {
			count++
		}
	}
	if count == 0 {
		return ""
	}
	return fmt.Sprintf("enrichment lookups: %d unavailable", count)
}

func discover(name string) string {
	for _, dir := range []string{".", "/usr/share/bandwidth-monitor", "/opt/bandwidth-monitor"} {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func discoverCity() string {
	if path := discover("GeoLite2-City.mmdb"); path != "" {
		return path
	}
	return discover("GeoLite2-Country.mmdb")
}
