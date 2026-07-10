package bandwidthtop

import (
	"strings"
	"testing"
	"unicode"
)

func TestFormatRateUsesBitsPerSecond(t *testing.T) {
	if got := FormatRate(125000); got != "1.0 Mbit/s" {
		t.Fatalf("got %q", got)
	}
	if got := formatCompactRate(125000); got != "1.00Mb" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("abcdefgh", 5); got != "abcd~" {
		t.Fatalf("got %q", got)
	}
	if got := Truncate("x", 0); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestRenderShowsLinkedDirectionsRollingRatesAndMetadata(t *testing.T) {
	got := RenderSnapshot([]Row{testFlowRow()}, testTotals(), 160)
	for _, want := range []string{
		"=>", "<=", "2s", "10s", "40s", "AS64500",
		"Example Networks", "Exampleland", "PKTS", "42", "TX", "RX", "TOTAL",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "192.0.2.10") != 2 {
		t.Fatalf("local endpoint not linked across two rows:\n%s", got)
	}
}

func TestRenderAggregatesUseSuppliedAllPeerTotals(t *testing.T) {
	totals := Totals{
		Tx: RateWindows{Two: 125000, Ten: 250000, Forty: 375000},
		Rx: RateWindows{Two: 250000, Ten: 500000, Forty: 750000},
	}
	got := RenderSnapshot(nil, totals, 80)
	if !strings.Contains(got, "TX") || !strings.Contains(got, "1.00Mb") ||
		!strings.Contains(got, "TOTAL") || !strings.Contains(got, "3.00Mb") {
		t.Fatalf("unexpected aggregate footer:\n%s", got)
	}
}

func TestRateBarScalesAndShowsEveryNonzeroRate(t *testing.T) {
	if got := rateBar(0, 100, 10); got != ".........." {
		t.Fatalf("zero bar %q", got)
	}
	if got := rateBar(1, 100, 10); got != "#........." {
		t.Fatalf("small nonzero bar %q", got)
	}
	if got := rateBar(50, 100, 10); got != "#####....." {
		t.Fatalf("half bar %q", got)
	}
	if got := rateBar(100, 100, 10); got != "##########" {
		t.Fatalf("full bar %q", got)
	}
}

func TestRenderANSIOnlyInLiveMode(t *testing.T) {
	snapshot := RenderSnapshot([]Row{testFlowRow()}, testTotals(), 80)
	if strings.Contains(snapshot, "\x1b") {
		t.Fatalf("snapshot contains ANSI: %q", snapshot)
	}
	live := RenderLive([]Row{testFlowRow()}, testTotals(), 80)
	if !strings.Contains(live, ansiGreen) || !strings.Contains(live, ansiCyan) {
		t.Fatalf("live output lacks restrained direction colors: %q", live)
	}
	if stripRenderANSI(live) != snapshot[:len(snapshot)-len("snapshot complete\n")]+"Ctrl-C / SIGTERM quit\n" {
		t.Fatalf("ANSI mode changed content beyond valid hint/coloring")
	}
}

func TestRenderNeverExceedsRequestedWidth(t *testing.T) {
	rows := []Row{testFlowRow()}
	for width := 1; width <= 200; width++ {
		for _, output := range []string{
			RenderSnapshot(rows, testTotals(), width),
			stripRenderANSI(RenderLive(rows, testTotals(), width)),
		} {
			for _, line := range strings.Split(strings.TrimSuffix(output, "\n"), "\n") {
				if count := displayWidth(line); count > width {
					t.Fatalf("width %d produced %d columns: %q", width, count, line)
				}
			}
		}
	}
}

func TestRenderHandlesWideRunesWithinBounds(t *testing.T) {
	row := testFlowRow()
	row.Info.Hostname = "peer界界界.example"
	row.Info.Provider = "Example界Network界Organization"
	for _, width := range []int{42, 80, 120, 160} {
		got := RenderSnapshot([]Row{row}, testTotals(), width)
		for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
			if count := displayWidth(line); count > width {
				t.Fatalf("width %d produced %d columns: %q", width, count, line)
			}
		}
	}
}

func TestRenderStripsTerminalAndUnicodeControls(t *testing.T) {
	hostile := "\x1b[31mred\x1b[0m\n\x1b]0;title\a\x1bPsecret\x1b\\\u202Eevil\u009b32mgreen"
	row := testFlowRow()
	row.LocalIP = hostile
	row.Stat.IP = hostile
	row.Stat.Hostname = hostile
	row.Info = Enrichment{Hostname: hostile, Provider: hostile, Country: hostile, Source: hostile}
	got := RenderSnapshot([]Row{row}, testTotals(), 160)
	if strings.Contains(got, "\x1b") || strings.Contains(got, "31m") ||
		strings.Contains(got, "title") || strings.Contains(got, "secret") ||
		strings.Contains(got, "32m") {
		t.Fatalf("terminal sequence survived sanitization: %q", got)
	}
	for _, r := range got {
		if r != '\n' && (unicode.IsControl(r) || unicode.In(r, unicode.Cf)) {
			t.Fatalf("control rune %U survived sanitization", r)
		}
	}
}

func testFlowRow() Row {
	return Row{
		LocalIP: "192.0.2.10",
		Stat: Stat{
			IP: "198.51.100.20", Hostname: "peer.example",
			Tx:      RateWindows{Two: 125000, Ten: 62500, Forty: 31250},
			Rx:      RateWindows{Two: 250000, Ten: 125000, Forty: 62500},
			Packets: 42,
		},
		Info: Enrichment{
			Hostname: "peer.example", Provider: "Example Networks",
			Country: "Exampleland", ASN: 64500, Source: "local+monitor",
		},
	}
}

func testTotals() Totals {
	row := testFlowRow()
	return Totals{Tx: row.Stat.Tx, Rx: row.Stat.Rx}
}

func stripRenderANSI(value string) string {
	for _, sequence := range []string{ansiReset, ansiBold, ansiDim, ansiGreen, ansiCyan} {
		value = strings.ReplaceAll(value, sequence, "")
	}
	return value
}
