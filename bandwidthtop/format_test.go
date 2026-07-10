package bandwidthtop

import (
	"strings"
	"testing"
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
	got := Render([]Row{{LocalIP: "192.0.2.10", Stat: Stat{IP: "198.51.100.20", RateBytes: 125000, Packets: 3}}}, 40)
	if !strings.Contains(got, "1.0 Mbit/s") || strings.Contains(got, "PROVIDER") {
		t.Fatalf("unexpected output:\n%s", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if len(line) > 40 {
			t.Fatalf("line is %d columns: %q", len(line), line)
		}
	}
}
