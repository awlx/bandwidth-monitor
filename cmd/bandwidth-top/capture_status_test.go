//go:build linux || darwin || freebsd

package main

import (
	"bytes"
	"strings"
	"testing"

	"bandwidth-monitor/bandwidthtop"
	"bandwidth-monitor/packets"
	"bandwidth-monitor/talkers"
)

type fakeCaptureSource struct {
	health []talkers.CaptureHealth
	stats  map[string]packets.RingStats
}

func (s fakeCaptureSource) CaptureHealth() []talkers.CaptureHealth     { return s.health }
func (s fakeCaptureSource) CaptureStats() map[string]packets.RingStats { return s.stats }

func TestCaptureStatusLine(t *testing.T) {
	stats := map[string]packets.RingStats{
		"test0": {Packets: 1_500_000, Drops: 30},
		"test1": {Packets: 500, Drops: 2},
	}
	for _, tc := range []struct {
		name   string
		source captureStatsSource
		want   string
	}{
		{name: "nil source", source: nil, want: ""},
		{name: "no stats", source: fakeCaptureSource{}, want: ""},
		{
			name:   "no window yet",
			source: fakeCaptureSource{stats: stats},
			want:   "capture: 0 pkt/s, 0 drop/s (0.00%) | since start: 1.5M pkt, 32 drop",
		},
		{
			name: "ok",
			source: fakeCaptureSource{stats: stats, health: []talkers.CaptureHealth{
				{PacketsPerSec: 12_000, Status: "ok"},
				{PacketsPerSec: 500, Status: "ok"},
			}},
			want: "capture: 12.5k pkt/s, 0 drop/s (0.00%) | since start: 1.5M pkt, 32 drop",
		},
		{
			name: "warn",
			source: fakeCaptureSource{stats: stats, health: []talkers.CaptureHealth{
				{PacketsPerSec: 9_990, DropsPerSec: 10, Status: "warn"},
				{Status: "ok"},
			}},
			want: "WARN capture: 10.0k pkt/s, 10 drop/s (0.10%) | since start: 1.5M pkt, 32 drop",
		},
		{
			name: "alert wins over warn",
			source: fakeCaptureSource{stats: stats, health: []talkers.CaptureHealth{
				{PacketsPerSec: 900, DropsPerSec: 100, Status: "alert"},
				{PacketsPerSec: 1000, DropsPerSec: 1, Status: "warn"},
			}},
			want: "ALERT capture: 1.9k pkt/s, 101 drop/s (5.05%) | since start: 1.5M pkt, 32 drop",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := captureStatus(tc.source); got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestFormatCount(t *testing.T) {
	for value, want := range map[float64]string{
		-1: "0", 0: "0", 999: "999", 1000: "1.0k", 12_345: "12.3k",
		2_500_000: "2.5M", 7e9: "7.0G", 3e12: "3.0T", 4e15: "4000.0T",
	} {
		if got := formatCount(value); got != want {
			t.Fatalf("formatCount(%v)=%q, want %q", value, got, want)
		}
	}
}

func TestServicesOptions(t *testing.T) {
	for _, args := range [][]string{{"-S"}, {"--services"}} {
		options, err := parseCLIOptions(args, &bytes.Buffer{})
		if err != nil || !options.services || options.ports {
			t.Fatalf("%v: options=%+v err=%v", args, options, err)
		}
	}
	var stdout, stderr bytes.Buffer
	err := run([]string{"-P", "-S"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "choose one of --ports and --services") {
		t.Fatalf("got %v", err)
	}
	err = run([]string{"--help"}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "-S") || !strings.Contains(stderr.String(), "services") {
		t.Fatalf("help missing services option:\n%s", stderr.String())
	}
}

type countingEnricher struct{ lookups int }

func (e *countingEnricher) Lookup(string) bandwidthtop.Enrichment {
	e.lookups++
	return bandwidthtop.Enrichment{}
}

func TestServiceRowsSkipEnrichmentAndPTR(t *testing.T) {
	enricher := &countingEnricher{}
	resolver := &mappingTestResolver{enabled: true, lookups: make(map[string]int)}
	rows, _ := mapSnapshotRows([]talkers.DirectTalkerStat{{
		Service:    "HTTPS",
		TalkerStat: talkers.TalkerStat{RxBytes: 4096, TxBytes: 1024, RxRate: 10, TxRate: 5},
	}}, talkers.DirectRateTotals{}, enricher, resolver, false, bandwidthtop.ViewServices)
	if len(rows) != 1 || rows[0].Service != "HTTPS" || rows[0].RxBytes != 4096 ||
		rows[0].TxBytes != 1024 || rows[0].Stat.Rx.Two != 10 || rows[0].Stat.Tx.Two != 5 {
		t.Fatalf("rows=%+v", rows)
	}
	if enricher.lookups != 0 || len(resolver.lookups) != 0 {
		t.Fatalf("services view looked up endpoints: enrich=%d ptr=%v",
			enricher.lookups, resolver.lookups)
	}
}
