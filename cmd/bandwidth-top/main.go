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
	"bandwidth-monitor/geoip"
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
	publicURL := fs.String("public-url", bandwidthtop.DefaultPublicURL, "public enrichment API base URL")
	noPublic := fs.Bool("no-public", false, "disable public enrichment fallback")
	width := fs.Int("width", 0, "output width in columns (default: terminal width)")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: bandwidth-top [options]")
		fmt.Fprintln(stderr, "Live AF_PACKET traffic viewer; requires root or CAP_NET_RAW.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *rows <= 0 || *refresh <= 0 {
		return fmt.Errorf("rows and refresh must be positive")
	}

	iface, localNets, _, err := bandwidthtop.SelectInterface(*ifaceName)
	if err != nil {
		return err
	}
	db, err := geoip.Open(*cityPath, *asnPath)
	if err != nil {
		return err
	}
	defer db.Close()
	enricher, err := bandwidthtop.NewEnricher(bandwidthtop.Config{
		GeoDB: db, ServerURL: *server, PublicURL: *publicURL, DisablePublic: *noPublic,
	})
	if err != nil {
		return err
	}
	defer enricher.Close()
	dns := resolver.New()
	defer dns.Stop()
	tracker := talkers.NewDirect(iface.Name, false, localNets, nil, dns)
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
		stats := tracker.DirectTopByBandwidth(*rows)
		viewRows := make([]bandwidthtop.Row, 0, len(stats))
		for _, stat := range stats {
			viewRows = append(viewRows, bandwidthtop.Row{
				LocalIP: stat.LocalIP,
				Stat: bandwidthtop.Stat{
					IP: stat.IP, Hostname: stat.Hostname, RxRate: stat.RxRate,
					TxRate: stat.TxRate, RateBytes: stat.RateBytes, Packets: stat.Packets,
				},
				Info: enricher.Lookup(stat.IP),
			})
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
		title := fmt.Sprintf("bandwidth-top  interface=%s  refresh=%s", iface.Name, refresh.String())
		fmt.Fprintln(stdout, bandwidthtop.Truncate(title, frameWidth))
		fmt.Fprint(stdout, bandwidthtop.Render(viewRows, frameWidth))
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
