//go:build linux || darwin

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
	"bandwidth-monitor/version"

	tea "charm.land/bubbletea/v2"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "bandwidth-top:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	options, err := parseCLIOptions(args, stderr)
	if err != nil {
		return err
	}
	if options.showVersion {
		_, err := fmt.Fprintf(stdout, "bandwidth-top %s\n", version.String())
		return err
	}
	if options.rows <= 0 || options.refresh <= 0 {
		return fmt.Errorf("rows and refresh must be positive")
	}

	selected, err := bandwidthtop.SelectCaptureInterface(options.interfaceName)
	if err != nil {
		return err
	}
	iface := selected.Interface
	localNetworks := bandwidthtop.EffectiveLocalNetworks(
		selected.LocalNets, options.localNetworks.Networks())
	monitorURL, monitorDiscovery := bandwidthtop.MonitorServerURL(
		options.server, options.noServerDiscovery, selected.Gateway, iface.Name)
	enricher, err := bandwidthtop.NewEnricherWithDatabases(bandwidthtop.Config{
		ServerURL: monitorURL, PublicURL: options.publicURL,
		DisablePublic: options.noPublic, MonitorDiscovery: monitorDiscovery,
		DisableMonitorDiscovery: options.noServerDiscovery,
	}, options.cityPath, options.asnPath)
	if err != nil {
		return err
	}
	defer enricher.Close()
	for _, warning := range enricher.StartupWarnings() {
		fmt.Fprintln(stderr, "bandwidth-top:", warning)
	}
	liveTerminal := supportsLiveTerminal(stdout, options.snapshot, os.Getenv("TERM"))
	if !options.snapshot && !liveTerminal {
		fmt.Fprintln(stderr, "bandwidth-top: non-interactive terminal; rendering one plain snapshot")
	}
	viewMode := bandwidthtop.ViewHosts
	if options.ports {
		viewMode = bandwidthtop.ViewPorts
	}
	log.SetOutput(io.Discard)
	dns := resolver.New()
	dns.SetEnabled(!options.noResolve)
	defer dns.Stop()
	tracker := talkers.NewDirect(iface.Name, false, localNetworks, nil, dns)
	go tracker.Run()
	defer tracker.Stop()

	title := fmt.Sprintf("bandwidth-top  interface=%s  refresh=%s  rates=bit/s",
		iface.Name, options.refresh.String())
	if liveTerminal {
		done := make(chan struct{})
		model := newLiveModel(liveModelConfig{
			title: title, rows: options.rows, refresh: options.refresh, width: options.width,
			noResolve: options.noResolve, tracker: tracker, enricher: enricher, resolver: dns,
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

	return runSnapshot(
		stdout, tracker, enricher, dns, title, options.rows, options.refresh,
		options.width, options.noResolve, viewMode,
	)
}

type cliOptions struct {
	interfaceName     string
	localNetworks     bandwidthtop.LocalNetworkFlags
	rows              int
	refresh           time.Duration
	snapshot          bool
	ports             bool
	asnPath           string
	cityPath          string
	server            string
	noServerDiscovery bool
	publicURL         string
	noPublic          bool
	noResolve         bool
	width             int
	showVersion       bool
}

func parseCLIOptions(args []string, stderr io.Writer) (cliOptions, error) {
	var options cliOptions
	fs := flag.NewFlagSet("bandwidth-top", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&options.interfaceName, "interface", "", "capture interface (default: primary default route)")
	fs.StringVar(&options.interfaceName, "i", "", "capture interface (shorthand)")
	fs.Var(&options.localNetworks, "local-network", "local CIDR override (repeatable; replaces interface prefixes)")
	fs.IntVar(&options.rows, "rows", 20, "maximum rows")
	fs.IntVar(&options.rows, "L", 20, "maximum rows (shorthand)")
	fs.DurationVar(&options.refresh, "refresh", time.Second, "refresh interval")
	fs.BoolVar(&options.snapshot, "snapshot", false, "print one plain snapshot and exit")
	fs.BoolVar(&options.snapshot, "t", false, "print one plain snapshot and exit (shorthand)")
	fs.BoolVar(&options.ports, "ports", false, "aggregate by remote port and protocol (view: ports)")
	fs.BoolVar(&options.ports, "P", false, "aggregate by remote port and protocol (shorthand)")
	fs.StringVar(&options.asnPath, "asn-mmdb", discover("GeoLite2-ASN.mmdb"), "ASN MMDB path")
	fs.StringVar(&options.cityPath, "city-mmdb", discoverCity(), "city/country MMDB path")
	fs.StringVar(&options.server, "server", "", "bandwidth-monitor base URL for enrichment")
	fs.BoolVar(&options.noServerDiscovery, "no-server-discovery", false, "disable one-time default-gateway monitor discovery")
	fs.StringVar(&options.publicURL, "public-url", bandwidthtop.DefaultPublicURL, "public enrichment API base URL")
	fs.BoolVar(&options.noPublic, "no-public", false, "disable public enrichment fallback")
	fs.BoolVar(&options.noResolve, "no-resolve", false, "disable reverse DNS and show raw endpoint IPs")
	fs.BoolVar(&options.noResolve, "n", false, "disable reverse DNS and show raw endpoint IPs (shorthand)")
	fs.IntVar(&options.width, "width", 0, "output width in columns (default: terminal width)")
	fs.BoolVar(&options.showVersion, "version", false, "print version and exit")
	fs.BoolVar(&options.showVersion, "v", false, "print version and exit (shorthand)")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "bandwidth-top %s\n", version.String())
		fmt.Fprintln(stderr, "Usage: bandwidth-top [options]")
		fmt.Fprintln(stderr, captureUsageText())
		fmt.Fprintln(stderr, "Local databases and monitor readiness are checked once at startup.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	return options, nil
}

func runSnapshot(
	stdout io.Writer,
	tracker *talkers.Tracker,
	enricher *bandwidthtop.Enricher,
	dns resolverControl,
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

	viewRows, totals := snapshotRowsForMode(
		tracker, enricher, dns, rows, noResolve, mode)
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
