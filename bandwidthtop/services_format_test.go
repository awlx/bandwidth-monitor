package bandwidthtop

import (
	"strings"
	"testing"

	"github.com/rivo/uniseg"
)

func serviceTestRows() []Row {
	return []Row{
		{
			Service: "HTTPS", TxBytes: 2048, RxBytes: 10 << 20,
			Stat: Stat{
				Tx: RateWindows{Two: 1000, Ten: 900, Forty: 800},
				Rx: RateWindows{Two: 50000, Ten: 40000, Forty: 30000},
			},
		},
		{
			Service: "DNS", TxBytes: 512, RxBytes: 1024,
			Stat: Stat{Tx: RateWindows{Two: 10}, Rx: RateWindows{Two: 20}},
		},
	}
}

func TestViewModeCyclesHostsPortsServices(t *testing.T) {
	want := []ViewMode{ViewPorts, ViewServices, ViewHosts}
	mode := ViewHosts
	for _, next := range want {
		mode = mode.Next()
		if mode != next {
			t.Fatalf("got %v, want %v", mode, next)
		}
	}
	for mode, name := range map[ViewMode]string{
		ViewHosts: "hosts", ViewPorts: "ports", ViewServices: "services",
	} {
		if mode.String() != name {
			t.Fatalf("%d.String()=%q, want %q", mode, mode.String(), name)
		}
	}
	if ViewServices.HeaderMarker() != "SERVICE" || ViewPorts.HeaderMarker() != "LOCAL" {
		t.Fatal("unexpected header markers")
	}
}

func TestServicesViewShowsRatesAndSinceStartTotals(t *testing.T) {
	totals := Totals{TxBytes: 2560, RxBytes: 10<<20 + 1024, HasCumulative: true}
	out := RenderSnapshotForMode(serviceTestRows(), totals, 100, 20, ViewServices)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	header := ""
	for _, line := range lines {
		if strings.Contains(line, "SERVICE") {
			header = line
		}
	}
	for _, want := range []string{"#", "SERVICE", "GRAPH", "2s", "10s", "40s", "TOTAL"} {
		if !strings.Contains(header, want) {
			t.Fatalf("header %q missing %q:\n%s", header, want, out)
		}
	}
	for _, want := range []string{"HTTPS", "=>", "<=", "8.00Kb", "400Kb", "2.00 KiB", "10.0 MiB", "DNS", "SINCE START"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "LOCAL") || strings.Contains(out, "ASN") {
		t.Fatalf("services view has endpoint columns:\n%s", out)
	}
}

func TestServicesViewBoundsAtEveryWidth(t *testing.T) {
	rows := serviceTestRows()
	rows = append(rows, Row{Service: "\x1b[31mEvil\x1b[0m"}, Row{})
	for width := 1; width <= 140; width++ {
		for _, ansi := range []bool{false, true} {
			var out string
			if ansi {
				out = RenderLiveForMode(rows, Totals{HasCumulative: true}, width, 20, ViewServices)
			} else {
				out = RenderSnapshotForMode(rows, Totals{HasCumulative: true}, width, 20, ViewServices)
			}
			for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
				plain := stripANSIForTest(line)
				if got := uniseg.StringWidth(plain); got > width {
					t.Fatalf("width=%d ansi=%v line width=%d: %q", width, ansi, got, line)
				}
				if strings.Contains(plain, "\x1b") {
					t.Fatalf("unsanitized escape at width %d: %q", width, line)
				}
			}
			if width >= 60 && !strings.Contains(out, "Other") {
				t.Fatalf("empty service name not shown as Other at width %d:\n%s", width, out)
			}
		}
	}
}

func stripANSIForTest(s string) string {
	for _, code := range []string{ansiReset, ansiBold, ansiDim, ansiGreen, ansiCyan} {
		s = strings.ReplaceAll(s, code, "")
	}
	return s
}
