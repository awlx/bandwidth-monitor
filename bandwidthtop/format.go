package bandwidthtop

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

type RateWindows struct {
	Two   float64
	Ten   float64
	Forty float64
}

type Stat struct {
	IP       string
	Hostname string
	Rx       RateWindows
	Tx       RateWindows
	Packets  uint64
}

type Row struct {
	LocalIP string
	Stat    Stat
	Info    Enrichment
}

type Totals struct {
	Rx RateWindows
	Tx RateWindows
}

type flowLayout struct {
	rank, local, arrow, remote int
	asn, provider, graph, rate int
	showASN, showProvider      bool
	showGraph                  bool
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
)

func FormatRate(bytesPerSecond float64) string {
	bits := bytesPerSecond * 8
	units := []string{"bit/s", "Kbit/s", "Mbit/s", "Gbit/s", "Tbit/s"}
	i := 0
	for bits >= 1000 && i < len(units)-1 {
		bits /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", bits, units[i])
	}
	return fmt.Sprintf("%.1f %s", bits, units[i])
}

func formatCompactRate(bytesPerSecond float64) string {
	if bytesPerSecond < 0 {
		bytesPerSecond = 0
	}
	value := bytesPerSecond * 8
	units := []string{"b", "Kb", "Mb", "Gb", "Tb"}
	unit := 0
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	switch {
	case unit == 0:
		return fmt.Sprintf("%.0f%s", value, units[unit])
	case value < 10:
		return fmt.Sprintf("%.2f%s", value, units[unit])
	case value < 100:
		return fmt.Sprintf("%.1f%s", value, units[unit])
	default:
		return fmt.Sprintf("%.0f%s", value, units[unit])
	}
}

func Truncate(s string, width int) string {
	s = sanitizeTerminal(s)
	if width <= 0 {
		return ""
	}
	if displayWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "~"
	}
	var out strings.Builder
	rest := s
	state := -1
	used := 0
	for len(rest) > 0 {
		cluster, next, clusterWidth, nextState := uniseg.FirstGraphemeClusterInString(rest, state)
		if used+clusterWidth > width-1 {
			break
		}
		out.WriteString(cluster)
		used += clusterWidth
		rest, state = next, nextState
	}
	out.WriteByte('~')
	return out.String()
}

func sanitizeTerminal(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == '\x1b' {
			i += size
			if i >= len(s) {
				continue
			}
			next := s[i]
			switch next {
			case '[':
				i++
				for i < len(s) {
					b := s[i]
					i++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
			case ']':
				i++
				for i < len(s) {
					if s[i] == '\a' {
						i++
						break
					}
					if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			case 'P', 'X', '^', '_':
				i++
				for i < len(s) {
					if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			default:
				_, nextSize := utf8.DecodeRuneInString(s[i:])
				i += nextSize
			}
			continue
		}
		if r == '\u009b' {
			i += size
			for i < len(s) {
				b := s[i]
				i++
				if b >= 0x40 && b <= 0x7e {
					break
				}
			}
			continue
		}
		if r == '\u0090' || r == '\u0098' || r == '\u009d' ||
			r == '\u009e' || r == '\u009f' {
			i += size
			for i < len(s) {
				next, nextSize := utf8.DecodeRuneInString(s[i:])
				i += nextSize
				if next == '\a' || next == '\u009c' {
					break
				}
			}
			continue
		}
		i += size
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// Render keeps the plain snapshot behavior for callers that do not supply
// all-peer totals.
func Render(rows []Row, width int) string {
	return RenderSnapshot(rows, sumTotals(rows), width)
}

func RenderSnapshot(rows []Row, totals Totals, width int) string {
	return RenderSnapshotWithRowLimit(rows, totals, width, len(rows))
}

func RenderLive(rows []Row, totals Totals, width int) string {
	return RenderLiveWithRowLimit(rows, totals, width, len(rows))
}

func RenderSnapshotWithRowLimit(rows []Row, totals Totals, width, maxRows int) string {
	return renderFlows(rows, totals, width, maxRows, false)
}

func RenderLiveWithRowLimit(rows []Row, totals Totals, width, maxRows int) string {
	return renderFlows(rows, totals, width, maxRows, true)
}

func renderFlows(rows []Row, totals Totals, width, maxRows int, ansi bool) string {
	if width <= 0 {
		width = 120
	}
	layout, structured := makeFlowLayout(width, rankWidth(maxRows))
	maxRate := maximumDirectionalRate(rows)
	var b strings.Builder
	if structured {
		if layout.showGraph {
			b.WriteString(color(ansiDim, flowLine(layout, "", "", "", "", "", "", graphRuler(maxRate, layout.graph), "", "", ""), ansi))
			b.WriteByte('\n')
		}
		header := flowLine(layout, "#", "LOCAL", "  ", "REMOTE", "ASN", "PROVIDER", "GRAPH", "2s", "10s", "40s")
		b.WriteString(color(ansiBold, header, ansi))
		b.WriteByte('\n')
		for i, row := range rows {
			rank := fmt.Sprintf("%d", i+1)
			outBar := rateBar(row.Stat.Tx.Two, maxRate, layout.graph)
			inBar := rateBar(row.Stat.Rx.Two, maxRate, layout.graph)
			b.WriteString(flowLineStyled(layout, rank, row.LocalIP, "=>", remoteHost(row),
				formatASN(row.Info.ASN), row.Info.Provider, outBar,
				formatCompactRate(row.Stat.Tx.Two), formatCompactRate(row.Stat.Tx.Ten),
				formatCompactRate(row.Stat.Tx.Forty),
				ansiGreen, ansi))
			b.WriteByte('\n')
			b.WriteString(flowLineStyled(layout, "", "", "<=", "", "", "", inBar,
				formatCompactRate(row.Stat.Rx.Two), formatCompactRate(row.Stat.Rx.Ten),
				formatCompactRate(row.Stat.Rx.Forty), ansiCyan, ansi))
			b.WriteByte('\n')
		}
	} else {
		b.WriteString(Truncate("# LOCAL => REMOTE 2s", width))
		b.WriteByte('\n')
		for i, row := range rows {
			out := fmt.Sprintf("%d %s => %s %s %s", i+1, row.LocalIP,
				remoteHost(row), rateBar(row.Stat.Tx.Two, maxRate, max(1, width/8)),
				formatCompactRate(row.Stat.Tx.Two))
			in := fmt.Sprintf("  <= %s %s", rateBar(row.Stat.Rx.Two, maxRate, max(1, width/8)),
				formatCompactRate(row.Stat.Rx.Two))
			b.WriteString(color(ansiGreen, Truncate(out, width), ansi))
			b.WriteByte('\n')
			b.WriteString(color(ansiCyan, Truncate(in, width), ansi))
			b.WriteByte('\n')
		}
	}
	b.WriteString(color(ansiDim, strings.Repeat("-", width), ansi))
	b.WriteByte('\n')
	b.WriteString(footerLine("TX", totals.Tx, width, ansiGreen, ansi))
	b.WriteByte('\n')
	b.WriteString(footerLine("RX", totals.Rx, width, ansiCyan, ansi))
	b.WriteByte('\n')
	b.WriteString(footerLine("TOTAL", addRates(totals.Tx, totals.Rx), width, ansiBold, ansi))
	b.WriteByte('\n')
	hint := "snapshot complete"
	if ansi {
		hint = "Ctrl-C / SIGTERM quit"
	}
	b.WriteString(color(ansiDim, Truncate(hint, width), ansi))
	b.WriteByte('\n')
	return b.String()
}

func rankWidth(maxRows int) int {
	if maxRows < 1 {
		maxRows = 1
	}
	return max(2, len(strconv.Itoa(maxRows)))
}

func makeFlowLayout(width, rank int) (flowLayout, bool) {
	layout := flowLayout{rank: rank, local: 7, arrow: 2, remote: 7, graph: 5, rate: 6}
	asnOnly := layout
	asnOnly.showASN, asnOnly.asn = true, 12
	asnGraph := asnOnly
	asnGraph.showGraph = true
	all := asnGraph
	all.showProvider, all.provider = true, 8
	graphOnly := layout
	graphOnly.showGraph = true
	switch {
	case width >= flowLayoutWidth(all)+16:
		layout = all
	case width >= flowLayoutWidth(asnGraph):
		layout = asnGraph
	case width >= flowLayoutWidth(asnOnly):
		layout = asnOnly
	case width >= flowLayoutWidth(graphOnly):
		layout = graphOnly
	case width < flowLayoutWidth(layout):
		return flowLayout{}, false
	}
	extra := width - flowLayoutWidth(layout)
	growColumnsEven(&layout.local, &layout.remote, 15, &extra)
	if layout.showProvider {
		growColumn(&layout.provider, 16, &extra)
	}
	growColumnsEven(&layout.local, &layout.remote, 39, &extra)
	if layout.showGraph {
		growColumn(&layout.graph, 24, &extra)
	}
	if layout.showProvider {
		growColumn(&layout.provider, 24, &extra)
	}
	if layout.showGraph {
		growColumn(&layout.graph, 40, &extra)
	}
	if layout.showProvider {
		growColumn(&layout.provider, 40, &extra)
	}
	return layout, true
}

func growColumn(column *int, limit int, extra *int) {
	amount := min(limit-*column, *extra)
	if amount > 0 {
		*column += amount
		*extra -= amount
	}
}

func growColumnsEven(left, right *int, limit int, extra *int) {
	for *extra > 0 && (*left < limit || *right < limit) {
		if *left <= *right && *left < limit || *right >= limit {
			*left++
		} else {
			*right++
		}
		*extra--
	}
}

func flowLayoutWidth(layout flowLayout) int {
	widths := layout.widths()
	total := len(widths) - 1
	for _, width := range widths {
		total += width
	}
	return total
}

func (layout flowLayout) widths() []int {
	widths := []int{layout.rank, layout.local, layout.arrow, layout.remote}
	if layout.showASN {
		widths = append(widths, layout.asn)
	}
	if layout.showProvider {
		widths = append(widths, layout.provider)
	}
	if layout.showGraph {
		widths = append(widths, layout.graph)
	}
	return append(widths, layout.rate, layout.rate, layout.rate)
}

func flowLine(layout flowLayout, rank, local, arrow, remote, asn, provider, graph, rate2, rate10, rate40 string) string {
	values := []string{rank, local, arrow, remote}
	if layout.showASN {
		values = append(values, asn)
	}
	if layout.showProvider {
		values = append(values, provider)
	}
	if layout.showGraph {
		values = append(values, graph)
	}
	values = append(values, rate2, rate10, rate40)
	widths := layout.widths()
	cells := make([]string, len(values))
	for i := range values {
		cells[i] = fitCell(values[i], widths[i], i == 0 || i >= len(values)-3)
	}
	return strings.Join(cells, " ")
}

func flowLineStyled(layout flowLayout, rank, local, arrow, remote, asn, provider, graph, rate2, rate10, rate40, style string, ansi bool) string {
	if !ansi {
		return flowLine(layout, rank, local, arrow, remote, asn, provider, graph, rate2, rate10, rate40)
	}
	values := []string{rank, local, arrow, remote}
	if layout.showASN {
		values = append(values, asn)
	}
	if layout.showProvider {
		values = append(values, provider)
	}
	if layout.showGraph {
		values = append(values, graph)
	}
	values = append(values, rate2, rate10, rate40)
	widths := layout.widths()
	cells := make([]string, len(values))
	for i := range values {
		cells[i] = fitCell(values[i], widths[i], i == 0 || i >= len(values)-3)
	}
	cells[0] = color(ansiBold, cells[0], true)
	cells[2] = color(style, cells[2], true)
	if layout.showGraph {
		cells[len(cells)-4] = color(style, cells[len(cells)-4], true)
	}
	return strings.Join(cells, " ")
}

func fitCell(value string, width int, right bool) string {
	value = Truncate(value, width)
	padding := strings.Repeat(" ", max(0, width-displayWidth(value)))
	if right {
		return padding + value
	}
	return value + padding
}

func rateBar(rate, maximum float64, width int) string {
	if width <= 0 {
		return ""
	}
	filled := 0
	if rate > 0 && maximum > 0 {
		filled = int(math.Ceil(rate / maximum * float64(width)))
		filled = max(1, min(width, filled))
	}
	return strings.Repeat("#", filled) + strings.Repeat(" ", width-filled)
}

func graphRuler(maximum float64, width int) string {
	if width <= 0 {
		return ""
	}
	label := formatCompactRate(maximum)
	if width < displayWidth(label)+1 {
		label = formatScaleRate(maximum)
	}
	if width < displayWidth(label)+2 {
		if width < displayWidth(label)+1 {
			return strings.Repeat("-", width)
		}
		return "0" + label
	}
	return "0" + strings.Repeat("-", width-displayWidth(label)-1) + label
}

func formatScaleRate(bytesPerSecond float64) string {
	value := math.Max(0, bytesPerSecond*8)
	units := []string{"b", "K", "M", "G", "T"}
	unit := 0
	for value >= 1000 && unit < len(units)-1 {
		value /= 1000
		unit++
	}
	return strconv.FormatFloat(value, 'g', 2, 64) + units[unit]
}

func maximumDirectionalRate(rows []Row) float64 {
	var maximum float64
	for _, row := range rows {
		maximum = math.Max(maximum, row.Stat.Tx.Two)
		maximum = math.Max(maximum, row.Stat.Rx.Two)
	}
	return maximum
}

func remoteHost(row Row) string {
	return sanitizeTerminal(row.Stat.IP)
}

func formatASN(asn uint) string {
	if asn == 0 || uint64(asn) > uint64(1<<32-1) {
		return "-"
	}
	return fmt.Sprintf("AS%d", asn)
}

func footerLine(label string, rates RateWindows, width int, style string, ansi bool) string {
	line := fmt.Sprintf("%-5s 2s %7s  10s %7s  40s %7s", label,
		formatCompactRate(rates.Two), formatCompactRate(rates.Ten),
		formatCompactRate(rates.Forty))
	return color(style, Truncate(line, width), ansi)
}

func sumTotals(rows []Row) Totals {
	var totals Totals
	for _, row := range rows {
		totals.Tx = addRates(totals.Tx, row.Stat.Tx)
		totals.Rx = addRates(totals.Rx, row.Stat.Rx)
	}
	return totals
}

func addRates(a, b RateWindows) RateWindows {
	return RateWindows{Two: a.Two + b.Two, Ten: a.Ten + b.Ten, Forty: a.Forty + b.Forty}
}

func color(code, value string, enabled bool) string {
	if !enabled || value == "" {
		return value
	}
	return code + value + ansiReset
}

func displayWidth(s string) int {
	return uniseg.StringWidth(s)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
