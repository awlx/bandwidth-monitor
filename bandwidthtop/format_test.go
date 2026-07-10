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
	if got := FormatRate(125); got != "1.0 Kbit/s" {
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

func TestRenderNarrowSnapshot(t *testing.T) {
	got := Render([]Row{{
		LocalIP: "192.0.2.10",
		Stat: Stat{
			IP: "198.51.100.20", RateBytes: 125000, Packets: 3,
		},
	}}, 40)
	if !strings.Contains(got, "1.0 Mbit/s") || strings.Contains(got, "PROVIDER") {
		t.Fatalf("unexpected output:\n%s", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if len(line) > 40 {
			t.Fatalf("line is %d columns: %q", len(line), line)
		}
	}
}

func TestRenderNeverExceedsRequestedWidth(t *testing.T) {
	row := Row{
		LocalIP: "192.0.2.10",
		Stat: Stat{
			IP: "2001:db8::1234", Hostname: "very-long-hostname.example",
			RxRate: 125000, TxRate: 250000, RateBytes: 375000, Packets: 123456,
		},
		Info: Enrichment{
			Hostname: "peer.example", Provider: "Example界Network Organization",
			Country: "Exampleland", ASN: 64500, Source: "local+monitor",
		},
	}
	for width := 1; width <= 152; width++ {
		got := Render([]Row{row}, width)
		for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
			if count := displayWidth(line); count > width {
				t.Fatalf("width %d produced %d columns: %q", width, count, line)
			}
		}
	}
}

func TestRenderStripsTerminalAndUnicodeControls(t *testing.T) {
	hostile := "\x1b[31mred\x1b[0m\n\x1b]0;title\a\x1bPsecret\x1b\\\u202Eevil\u009b32mgreen"
	got := Render([]Row{{
		LocalIP: hostile,
		Stat:    Stat{IP: "192.0.2.1", Hostname: hostile},
		Info:    Enrichment{Provider: hostile, Country: hostile, Source: hostile},
	}}, 152)
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
