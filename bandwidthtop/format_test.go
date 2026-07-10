package bandwidthtop

import (
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/rivo/uniseg"
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

func TestRemoteHostResolutionPreference(t *testing.T) {
	tests := []struct {
		name string
		row  Row
		want string
	}{
		{
			name: "PTR preferred",
			row: Row{
				Stat: Stat{IP: "198.51.100.20", Hostname: "ptr.example"},
				Info: Enrichment{Hostname: "enrichment.example"},
			},
			want: "ptr.example",
		},
		{
			name: "enrichment fallback",
			row: Row{
				Stat: Stat{IP: "198.51.100.20"},
				Info: Enrichment{Hostname: "enrichment.example"},
			},
			want: "enrichment.example",
		},
		{
			name: "raw IP fallback",
			row:  Row{Stat: Stat{IP: "198.51.100.20"}},
			want: "198.51.100.20",
		},
		{
			name: "sanitized PTR fallback",
			row: Row{
				Stat: Stat{IP: "198.51.100.20", Hostname: "\x1b[31m"},
				Info: Enrichment{Hostname: "enrichment.example"},
			},
			want: "enrichment.example",
		},
		{
			name: "sanitized hostname IP fallback",
			row: Row{
				Stat: Stat{IP: "198.51.100.20", Hostname: "\x1b[31m"},
				Info: Enrichment{Hostname: "\u202e"},
			},
			want: "198.51.100.20",
		},
		{
			name: "no resolve forces IP",
			row: Row{
				Stat:      Stat{IP: "198.51.100.20", Hostname: "ptr.example"},
				Info:      Enrichment{Hostname: "enrichment.example"},
				NoResolve: true,
			},
			want: "198.51.100.20",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := remoteHost(test.row); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestRemoteHostnameArrivalPreservesLayout(t *testing.T) {
	const width = 80
	row := testFlowRow()
	before := RenderSnapshot([]Row{row}, testTotals(), width)
	row.Stat.Hostname = "peer-with-a-long-local-ptr-name.example"
	after := RenderSnapshot([]Row{row}, testTotals(), width)

	beforeLines := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	afterLines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	if beforeLines[1] != afterLines[1] {
		t.Fatalf("async hostname changed columns:\n%s\n%s", before, after)
	}
	if strings.Index(beforeLines[2], "=>") != strings.Index(afterLines[2], "=>") {
		t.Fatalf("async hostname moved direction column:\n%s\n%s", before, after)
	}
	if displayWidth(beforeLines[2]) != width || displayWidth(afterLines[2]) != width {
		t.Fatalf("async hostname changed line width: before=%d after=%d",
			displayWidth(beforeLines[2]), displayWidth(afterLines[2]))
	}
	if !strings.Contains(afterLines[2], "peer-with-a-lon~") {
		t.Fatalf("hostname was not truncated in the REMOTE cell:\n%s", after)
	}
}

func TestRenderShowsClassicLinkedDirectionsAndDedicatedMetadata(t *testing.T) {
	got := RenderSnapshot([]Row{testFlowRow()}, testTotals(), 160)
	for _, want := range []string{
		"=>", "<=", "2s", "10s", "40s", "AS64500",
		"ASN", "PROVIDER", "Example Networks", "TX", "RX", "TOTAL",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "192.0.2.10") != 1 ||
		strings.Count(got, "198.51.100.20") != 1 ||
		strings.Count(got, "AS64500") != 1 ||
		strings.Count(got, "Example Networks") != 1 {
		t.Fatalf("pair metadata should appear only on the primary line:\n%s", got)
	}
	if strings.Contains(got, "Exampleland") || strings.Contains(got, "local+monitor") {
		t.Fatalf("optional metadata was merged into the flow screen:\n%s", got)
	}
}

func TestRenderSnapshotLayoutsAtRepresentativeWidths(t *testing.T) {
	for _, width := range []int{80, 120, 160} {
		got := RenderSnapshot([]Row{testFlowRow()}, testTotals(), width)
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		if len(lines) < 5 {
			t.Fatalf("width %d produced incomplete snapshot:\n%s", width, got)
		}
		header := lines[1]
		primary := lines[2]
		continuation := lines[3]
		for _, heading := range []string{"LOCAL", "REMOTE", "ASN", "GRAPH", "2s", "10s", "40s"} {
			if !strings.Contains(header, heading) {
				t.Fatalf("width %d missing dedicated %s column:\n%s", width, heading, got)
			}
		}
		if width >= 86 && !strings.Contains(header, "PROVIDER") {
			t.Fatalf("width %d missing dedicated provider column:\n%s", width, got)
		}
		for _, value := range []string{"198.51", "AS64500"} {
			if !strings.Contains(primary, value) || strings.Contains(continuation, value) {
				t.Fatalf("width %d did not isolate %q to primary line:\n%s", width, value, got)
			}
		}
		if width >= 86 && (!strings.Contains(primary, "Example") || strings.Contains(continuation, "Example")) {
			t.Fatalf("width %d did not isolate provider to primary line:\n%s", width, got)
		}
		for _, heading := range []string{"REMOTE", "ASN", "GRAPH", "2s"} {
			index := strings.Index(header, heading)
			if index < 0 || len(primary) <= index || len(continuation) <= index {
				t.Fatalf("width %d has unstable %s alignment:\n%s", width, heading, got)
			}
		}
		remoteStart := strings.Index(header, "REMOTE")
		graphStart := strings.Index(header, "GRAPH")
		if strings.TrimSpace(continuation[remoteStart:graphStart]) != "" {
			t.Fatalf("width %d continuation metadata columns are not blank:\n%s", width, got)
		}
		if strings.Index(lines[0], "0") != graphStart {
			t.Fatalf("width %d ruler is not aligned with graph lane:\n%s", width, got)
		}
		if strings.Index(primary, "=>") != strings.Index(continuation, "<=") {
			t.Fatalf("width %d arrows are not rigidly aligned:\n%s", width, got)
		}
	}
}

func TestRenderDropsOptionalColumnsWithoutMerging(t *testing.T) {
	asnOnly := RenderSnapshot([]Row{testFlowRow()}, testTotals(), 71)
	if !strings.Contains(asnOnly, "ASN") || strings.Contains(asnOnly, "PROVIDER") ||
		strings.Contains(asnOnly, "Example") {
		t.Fatalf("71-column layout did not drop provider wholesale:\n%s", asnOnly)
	}

	hostsOnly := RenderSnapshot([]Row{testFlowRow()}, testTotals(), 53)
	if strings.Contains(hostsOnly, "ASN") || strings.Contains(hostsOnly, "PROVIDER") ||
		strings.Contains(hostsOnly, "AS64500") || strings.Contains(hostsOnly, "Example") {
		t.Fatalf("53-column layout merged optional metadata:\n%s", hostsOnly)
	}
}

func TestRenderPreservesCommonIPWidthsAndGraphHeading(t *testing.T) {
	ipv4 := RenderSnapshot([]Row{testFlowRow()}, testTotals(), 80)
	if !strings.Contains(ipv4, "192.0.2.10") || !strings.Contains(ipv4, "198.51.100.20") {
		t.Fatalf("80-column layout truncated common IPv4 endpoints:\n%s", ipv4)
	}
	row := testFlowRow()
	row.LocalIP = "2001:db8:abcd:1234:5678:9abc:def0:1234"
	row.Stat.IP = "2001:db8:1234:5678:9abc:def0:1234:5678"
	ipv6 := RenderSnapshot([]Row{row}, testTotals(), 160)
	if !strings.Contains(ipv6, row.LocalIP) || !strings.Contains(ipv6, row.Stat.IP) {
		t.Fatalf("160-column layout truncated IPv6 endpoints:\n%s", ipv6)
	}
	for _, width := range []int{64, 77, 86} {
		lines := strings.Split(RenderSnapshot([]Row{testFlowRow()}, testTotals(), width), "\n")
		if len(lines) < 2 || !strings.Contains(lines[1], "GRAPH") {
			t.Fatalf("width %d truncated graph heading:\n%s", width, strings.Join(lines, "\n"))
		}
	}
}

func TestRenderNeverTruncatesShownASN(t *testing.T) {
	for _, asn := range []uint{1, 13335, 1<<32 - 1} {
		for _, width := range []int{71, 76, 77, 80, 85, 86, 120, 160} {
			row := testFlowRow()
			row.Info.ASN = asn
			row.Info.Provider = "Hostile Provider With Excessively Wide Content"
			got := RenderSnapshot([]Row{row}, testTotals(), width)
			want := formatASN(asn)
			if !strings.Contains(got, "ASN") {
				t.Fatalf("width %d unexpectedly omitted ASN column:\n%s", width, got)
			}
			if strings.Count(got, want) != 1 || strings.Contains(got, want[:len(want)-1]+"~") {
				t.Fatalf("width %d truncated ASN %q:\n%s", width, want, got)
			}
			if width < 86 && strings.Contains(got, "PROVIDER") {
				t.Fatalf("width %d retained provider ahead of ASN:\n%s", width, got)
			}
			if width == 86 && strings.Contains(got, row.Info.Provider) {
				t.Fatalf("provider was not truncated before ASN at boundary:\n%s", got)
			}
		}
	}
	if formatASN(0) != "-" {
		t.Fatalf("missing ASN placeholder=%q", formatASN(0))
	}
	oversized := uint64(1) << 32
	if strconv.IntSize == 64 && formatASN(uint(oversized)) != "-" {
		t.Fatalf("oversized ASN was not normalized: %q", formatASN(uint(oversized)))
	}
}

func TestRenderUsesStableExplicitColumnOffsets(t *testing.T) {
	const width = 120
	rows := make([]Row, 99)
	for i := range rows {
		rows[i] = testFlowRow()
	}
	rows[0].LocalIP = "192.0.2.10"
	rows[0].Stat.IP = "198.51.100.20"
	rows[8].LocalIP = "2001:db8:abcd:1234:5678:9abc:def0:1234"
	rows[8].Stat.IP = "2001:db8:1234:5678:9abc:def0:1234:5678"
	rows[9].LocalIP = "fe80::1%eth0"
	rows[9].Stat.IP = "fe80::2%eth0"
	rows[9].Info.Provider = "Wide界Provider界Name"
	rows[98].LocalIP = ""
	rows[98].Stat.IP = ""
	rows[98].Info = Enrichment{}

	got := RenderSnapshotWithRowLimit(rows, testTotals(), width, 99)
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	headerIndex := indexContaining(lines, "LOCAL")
	if headerIndex < 0 {
		t.Fatalf("missing header:\n%s", got)
	}
	layout, ok := makeFlowLayout(width, rankWidth(99))
	if !ok {
		t.Fatal("expected structured layout")
	}
	starts := layoutColumnStarts(layout)
	widths := layout.widths()
	for _, rank := range []int{1, 9, 10, 99} {
		primary := lines[headerIndex+1+2*(rank-1)]
		continuation := lines[headerIndex+2+2*(rank-1)]
		if displayCellAt(primary, starts[1]-1) != " " ||
			displayCellAt(continuation, starts[1]-1) != " " {
			t.Fatalf("rank and LOCAL concatenated at rank %d:\n%q\n%q", rank, primary, continuation)
		}
		if strings.TrimSpace(primary[:starts[1]-1]) != strconv.Itoa(rank) {
			t.Fatalf("rank %d is not right-aligned in fixed cell: %q", rank, primary)
		}
		if primary[starts[2]:starts[2]+2] != "=>" ||
			continuation[starts[2]:starts[2]+2] != "<=" {
			t.Fatalf("rank %d direction offsets moved:\n%q\n%q", rank, primary, continuation)
		}
		for i := 0; i < len(widths)-1; i++ {
			separator := starts[i] + widths[i]
			if displayCellAt(primary, separator) != " " ||
				displayCellAt(continuation, separator) != " " {
				t.Fatalf("rank %d missing separator after column %d:\n%q\n%q",
					rank, i, primary, continuation)
			}
		}
	}

	before := RenderSnapshotWithRowLimit(rows[:1], testTotals(), width, 99)
	rows[0].Info.Provider = "Provider Arrived Asynchronously"
	rows[0].Info.ASN = 1<<32 - 1
	after := RenderSnapshotWithRowLimit(rows[:1], testTotals(), width, 99)
	beforeLines := strings.Split(strings.TrimSuffix(before, "\n"), "\n")
	afterLines := strings.Split(strings.TrimSuffix(after, "\n"), "\n")
	beforeHeader := indexContaining(beforeLines, "LOCAL")
	afterHeader := indexContaining(afterLines, "LOCAL")
	if beforeLines[beforeHeader] != afterLines[afterHeader] {
		t.Fatalf("stable content update changed layout:\n%s\n%s", before, after)
	}
	if strings.Index(beforeLines[beforeHeader+1], "=>") !=
		strings.Index(afterLines[afterHeader+1], "=>") {
		t.Fatalf("stable content update moved direction column:\n%s\n%s", before, after)
	}
}

func layoutColumnStarts(layout flowLayout) []int {
	widths := layout.widths()
	starts := make([]int, len(widths))
	for i := 1; i < len(widths); i++ {
		starts[i] = starts[i-1] + widths[i-1] + 1
	}
	return starts
}

func displayCellAt(value string, target int) string {
	position := 0
	rest := value
	state := -1
	for len(rest) > 0 {
		cluster, next, width, nextState := uniseg.FirstGraphemeClusterInString(rest, state)
		if target >= position && target < position+width {
			return cluster
		}
		position += width
		rest, state = next, nextState
	}
	return ""
}

func indexContaining(lines []string, value string) int {
	for i, line := range lines {
		if strings.Contains(line, value) {
			return i
		}
	}
	return -1
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
	if got := rateBar(0, 100, 10); got != "          " {
		t.Fatalf("zero bar %q", got)
	}
	if got := rateBar(1, 100, 10); got != "#         " {
		t.Fatalf("small nonzero bar %q", got)
	}
	if got := rateBar(50, 100, 10); got != "#####     " {
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
		if width >= 80 && !strings.Contains(got, "ASN") {
			t.Fatalf("width %d lost dedicated metadata headings:\n%s", width, got)
		}
		if width >= 86 && !strings.Contains(got, "PROVIDER") {
			t.Fatalf("width %d lost provider heading:\n%s", width, got)
		}
	}
}

func TestRenderMeasuresEmojiGraphemeWidths(t *testing.T) {
	if got := displayWidth("❤️"); got != 2 {
		t.Fatalf("VS16 emoji width=%d, want 2", got)
	}
	if got := displayWidth("👍🏽"); got != 2 {
		t.Fatalf("modified emoji width=%d, want 2", got)
	}
	row := testFlowRow()
	row.Info.Provider = "Network 👍🏽 ❤️ Provider"
	for width := 1; width <= 160; width++ {
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
	if strings.Count(got, "redgreen") > 2 {
		t.Fatalf("sanitized values bled into continuation columns: %q", got)
	}
}

func TestRemoteHostnameIsSanitizedBeforeTruncation(t *testing.T) {
	row := testFlowRow()
	row.Stat.Hostname = "\x1b[31mhostname-with-a-very-long-label.example\x1b[0m"
	got := RenderSnapshot([]Row{row}, testTotals(), 80)
	if strings.Contains(got, "\x1b") || strings.Contains(got, "31m") {
		t.Fatalf("terminal control sequence survived: %q", got)
	}
	if !strings.Contains(got, "hostname-with-a~") {
		t.Fatalf("sanitized hostname was not truncated to the REMOTE cell:\n%s", got)
	}
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		if displayWidth(line) > 80 {
			t.Fatalf("line exceeded requested width: %q", line)
		}
	}
}

func TestHostModeAPIIsByteCompatible(t *testing.T) {
	rows := []Row{testFlowRow()}
	want := RenderSnapshotWithRowLimit(rows, testTotals(), 120, 20)
	got := RenderSnapshotForMode(rows, testTotals(), 120, 20, ViewHosts)
	if got != want {
		t.Fatalf("host mode changed:\n%s\nwant:\n%s", got, want)
	}
}

func TestPortModeDedicatedColumnsAtRepresentativeWidths(t *testing.T) {
	row := testFlowRow()
	row.Port = "65535"
	row.Protocol = "TCP"
	for _, width := range []int{80, 120, 160} {
		got := RenderSnapshotForMode([]Row{row}, testTotals(), width, 20, ViewPorts)
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		header := lines[1]
		primary := lines[2]
		continuation := lines[3]
		for _, heading := range []string{"LOCAL", "REMOTE", "PORT", "PROTO", "ASN", "2s", "10s", "40s"} {
			if !strings.Contains(header, heading) {
				t.Fatalf("width %d missing %s:\n%s", width, heading, got)
			}
		}
		for _, value := range []string{"65535", "TCP", "AS64500"} {
			if strings.Count(primary, value) != 1 || strings.Contains(continuation, value) {
				t.Fatalf("width %d did not isolate %q:\n%s", width, value, got)
			}
		}
		for _, line := range lines {
			if displayWidth(line) > width {
				t.Fatalf("width %d produced %d columns: %q", width, displayWidth(line), line)
			}
		}
	}
}

func TestPortModeDropsOptionalColumnsWholeAndPreservesASN(t *testing.T) {
	row := testFlowRow()
	row.Port, row.Protocol = "443", "TCP"
	row.Info.ASN = 1<<32 - 1
	row.Info.Provider = "Hostile Provider With Very Wide Text"
	for _, width := range []int{69, 74, 75, 83, 84} {
		got := RenderSnapshotForMode([]Row{row}, testTotals(), width, 20, ViewPorts)
		if !strings.Contains(got, "PORT") || !strings.Contains(got, "PROTO") {
			t.Fatalf("mandatory columns missing at %d:\n%s", width, got)
		}
		if strings.Contains(got, "ASN") && !strings.Contains(got, "AS4294967295") {
			t.Fatalf("ASN truncated at %d:\n%s", width, got)
		}
		if width < 84 && strings.Contains(got, "PROVIDER") {
			t.Fatalf("provider retained ahead of mandatory/full ASN at %d:\n%s", width, got)
		}
	}
}

func TestPortModeSanitizesAndValidatesFields(t *testing.T) {
	row := testFlowRow()
	row.Port = "70000"
	row.Protocol = "\x1b[31mTCP\n"
	got := RenderSnapshotForMode([]Row{row}, testTotals(), 120, 20, ViewPorts)
	if strings.Contains(got, "70000") || strings.Contains(got, "\x1b") || strings.Contains(got, "31m") {
		t.Fatalf("hostile port/protocol survived:\n%q", got)
	}
	if !strings.Contains(got, " - ") || !strings.Contains(got, "TCP") {
		t.Fatalf("port placeholder or protocol missing:\n%s", got)
	}
}

func TestPortModeStableOffsetsAndBoundsAtEveryWidth(t *testing.T) {
	row := testFlowRow()
	row.Port, row.Protocol = "443", "TCP"
	for width := 1; width <= 200; width++ {
		got := RenderSnapshotForMode([]Row{row}, testTotals(), width, 99, ViewPorts)
		lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
		for _, line := range lines {
			if gotWidth := displayWidth(line); gotWidth > width {
				t.Fatalf("width %d produced %d columns: %q", width, gotWidth, line)
			}
		}
		if _, structured := makePortFlowLayout(width, rankWidth(99)); !structured {
			continue
		}
		headerIndex := indexContaining(lines, "LOCAL")
		if headerIndex < 0 || !strings.Contains(lines[headerIndex], "PORT") ||
			!strings.Contains(lines[headerIndex], "PROTO") {
			t.Fatalf("width %d lost structured port headings:\n%s", width, got)
		}
		primary, continuation := lines[headerIndex+1], lines[headerIndex+2]
		if strings.Index(primary, "=>") != strings.Index(continuation, "<=") {
			t.Fatalf("width %d moved direction offset:\n%s", width, got)
		}
		for _, heading := range []string{"REMOTE", "PORT", "PROTO", "2s"} {
			if offset := strings.Index(lines[headerIndex], heading); offset < 0 ||
				len(primary) <= offset || len(continuation) <= offset {
				t.Fatalf("width %d has unstable %s column:\n%s", width, heading, got)
			}
		}
	}
}

func testFlowRow() Row {
	return Row{
		LocalIP: "192.0.2.10",
		Stat: Stat{
			IP:      "198.51.100.20",
			Tx:      RateWindows{Two: 125000, Ten: 62500, Forty: 31250},
			Rx:      RateWindows{Two: 250000, Ten: 125000, Forty: 62500},
			Packets: 42,
		},
		Info: Enrichment{
			Provider: "Example Networks",
			Country:  "Exampleland", ASN: 64500, Source: "local+monitor",
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
