//go:build linux

package main

import (
	"errors"
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

	tea "charm.land/bubbletea/v2"
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
	ports := fs.Bool("ports", false, "aggregate by remote port and protocol (view: ports)")
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
	if !*snapshot && !liveTerminal {
		fmt.Fprintln(stderr, "bandwidth-top: non-interactive terminal; rendering one plain snapshot")
	}
	viewMode := bandwidthtop.ViewHosts
	if *ports {
		viewMode = bandwidthtop.ViewPorts
	}
	log.SetOutput(io.Discard)
	dns := resolver.New()
	dns.SetEnabled(!noResolve)
	defer dns.Stop()
	tracker := talkers.NewDirect(iface.Name, false, localNetworks, nil, dns)
	go tracker.Run()
	defer tracker.Stop()

	title := fmt.Sprintf("bandwidth-top  interface=%s  refresh=%s  rates=bit/s",
		iface.Name, refresh.String())
	if liveTerminal {
		done := make(chan struct{})
		model := newLiveModel(liveModelConfig{
			title: title, rows: *rows, refresh: *refresh, width: *width,
			noResolve: noResolve, tracker: tracker, enricher: enricher, resolver: dns,
			initialMode: viewMode, done: done,
		})
		final, programErr := tea.NewProgram(
			model,
			tea.WithOutput(stdout),
			tea.WithEnvironment(os.Environ()),
		).Run()
		close(done)
		if errors.Is(programErr, tea.ErrInterrupted) {
			return nil
		}
		if programErr != nil {
			return programErr
		}
		if result, ok := final.(*liveModel); ok && result.err != nil {
			return result.err
		}
		return nil
	}

	return runSnapshot(stdout, tracker, enricher, title, *rows, *refresh, *width, noResolve, viewMode)
}

func runSnapshot(
	stdout io.Writer,
	tracker *talkers.Tracker,
	enricher *bandwidthtop.Enricher,
	title string,
	rows int,
	refresh time.Duration,
	width int,
	noResolve bool,
	mode bandwidthtop.ViewMode,
) error {
	delay := refresh
	if delay < 1200*time.Millisecond {
		delay = 1200 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-signals:
		return nil
	case err := <-tracker.Errors():
		return captureError(err)
	case <-timer.C:
	}

	viewRows, totals := snapshotRowsForMode(tracker, enricher, rows, noResolve, mode)
	enricher.Wait()
	for i := range viewRows {
		viewRows[i].Info = enricher.Lookup(viewRows[i].Stat.IP)
	}
	size := snapshotDimensions(stdout, width)
	status := enricher.SourceStatusLines(size.width)
	status = append(status, viewStatus(mode)+" | "+rdnsStatus(!noResolve))
	if lookupStatus := lookupErrorStatus(viewRows); lookupStatus != "" {
		status = append(status, lookupStatus)
	}
	_, err := fmt.Fprintln(stdout, composeFrameForMode(title, status, viewRows, totals, rows, size, false, mode))
	return err
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
