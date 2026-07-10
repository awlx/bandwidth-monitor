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

	"golang.org/x/sys/unix"
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
	rows := fs.Int("rows", 20, "maximum rows")
	refresh := fs.Duration("refresh", time.Second, "refresh interval")
	snapshot := fs.Bool("snapshot", false, "print one plain snapshot and exit")
	asnPath := fs.String("asn-mmdb", discover("GeoLite2-ASN.mmdb"), "ASN MMDB path")
	cityPath := fs.String("city-mmdb", discoverCity(), "city/country MMDB path")
	server := fs.String("server", "", "bandwidth-monitor base URL for enrichment")
	noServerDiscovery := fs.Bool("no-server-discovery", false, "disable one-time default-gateway monitor discovery")
	publicURL := fs.String("public-url", bandwidthtop.DefaultPublicURL, "public enrichment API base URL")
	noPublic := fs.Bool("no-public", false, "disable public enrichment fallback")
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
	dns := resolver.New()
	defer dns.Stop()
	tracker := talkers.NewDirect(iface.Name, false, selected.LocalNets, nil, dns)
	log.SetOutput(io.Discard)
	go tracker.Run()
	defer tracker.Stop()

	delay := *refresh
	if *snapshot && delay < 1200*time.Millisecond {
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
		return fmt.Errorf("capture %s failed: %w (run as root or grant CAP_NET_RAW)", iface.Name, err)
	case <-timer.C:
	}

	firstFrame := true
	for {
		stats, rateTotals := tracker.DirectBandwidthSnapshot(*rows)
		viewRows := make([]bandwidthtop.Row, 0, len(stats))
		for _, stat := range stats {
			viewRows = append(viewRows, bandwidthtop.Row{
				LocalIP: stat.LocalIP,
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
		if *snapshot {
			enricher.Wait()
			for i := range viewRows {
				viewRows[i].Info = enricher.Lookup(viewRows[i].Stat.IP)
			}
		}
		if !*snapshot {
			if firstFrame {
				fmt.Fprint(stdout, "\x1b[?25l")
			}
			fmt.Fprint(stdout, "\x1b[H\x1b[2J")
		}
		frameWidth := outputWidth(stdout, *width)
		title := fmt.Sprintf("bandwidth-top  interface=%s  refresh=%s  rates=bit/s  sources=%s",
			iface.Name, refresh.String(), enricher.SourceSummary())
		fmt.Fprintln(stdout, bandwidthtop.Truncate(title, frameWidth))
		if *snapshot {
			fmt.Fprint(stdout, bandwidthtop.RenderSnapshot(viewRows, totals, frameWidth))
		} else {
			fmt.Fprint(stdout, bandwidthtop.RenderLive(viewRows, totals, frameWidth))
		}
		firstFrame = false
		if *snapshot {
			return nil
		}
		timer.Reset(*refresh)
		select {
		case <-signals:
			fmt.Fprint(stdout, "\x1b[?25h")
			return nil
		case err := <-tracker.Errors():
			fmt.Fprint(stdout, "\x1b[?25h")
			return fmt.Errorf("capture %s failed: %w (run as root or grant CAP_NET_RAW)", iface.Name, err)
		case <-timer.C:
		}
	}
}

func outputWidth(w io.Writer, configured int) int {
	if configured > 0 {
		return configured
	}
	if file, ok := w.(*os.File); ok {
		if size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ); err == nil && size.Col > 0 {
			return int(size.Col)
		}
	}
	return 120
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
