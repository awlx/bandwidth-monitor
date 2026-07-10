package bandwidthtop

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
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
	rank, local, arrow, remote, bar int
	rate, packets                   int
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
	used := 0
	for _, r := range s {
		runeWidth := terminalRuneWidth(r)
		if used+runeWidth > width-1 {
			break
		}
		out.WriteRune(r)
		used += runeWidth
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
	return renderFlows(rows, totals, width, false)
}

func RenderLive(rows []Row, totals Totals, width int) string {
	return renderFlows(rows, totals, width, true)
}

func renderFlows(rows []Row, totals Totals, width int, ansi bool) string {
	if width <= 0 {
		width = 120
	}
	layout, structured := makeFlowLayout(width)
	maxRate := maximumDirectionalRate(rows)
	var b strings.Builder
	if structured {
		header := flowLine(layout, "#", "LOCAL", "  ", "REMOTE PEER", "RATE", "2s", "10s", "40s", "PKTS")
		b.WriteString(color(ansiBold, header, ansi))
		b.WriteByte('\n')
		b.WriteString(color(ansiDim, strings.Repeat("-", width), ansi))
		b.WriteByte('\n')
		for i, row := range rows {
			rank := fmt.Sprintf("%d", i+1)
			outRemote := remoteIdentity(row)
			inRemote := remoteMetadata(row)
			outBar := rateBar(row.Stat.Tx.Two, maxRate, layout.bar)
			inBar := rateBar(row.Stat.Rx.Two, maxRate, layout.bar)
			b.WriteString(flowLineStyled(layout, rank, row.LocalIP, "=>", outRemote, outBar,
				formatCompactRate(row.Stat.Tx.Two), formatCompactRate(row.Stat.Tx.Ten),
				formatCompactRate(row.Stat.Tx.Forty), strconv.FormatUint(row.Stat.Packets, 10),
				ansiGreen, ansi))
			b.WriteByte('\n')
			b.WriteString(flowLineStyled(layout, "|", row.LocalIP, "<=", inRemote, inBar,
				formatCompactRate(row.Stat.Rx.Two), formatCompactRate(row.Stat.Rx.Ten),
				formatCompactRate(row.Stat.Rx.Forty), "", ansiCyan, ansi))
			b.WriteByte('\n')
		}
	} else {
		b.WriteString(Truncate("# FLOWS  2s rate", width))
		b.WriteByte('\n')
		for i, row := range rows {
			out := fmt.Sprintf("%d %s => %s %s %s", i+1, row.LocalIP,
				remoteIdentity(row), rateBar(row.Stat.Tx.Two, maxRate, max(1, width/8)),
				formatCompactRate(row.Stat.Tx.Two))
			in := fmt.Sprintf("| %s <= %s %s %s", row.LocalIP,
				remoteMetadata(row), rateBar(row.Stat.Rx.Two, maxRate, max(1, width/8)),
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

func makeFlowLayout(width int) (flowLayout, bool) {
	layout := flowLayout{rank: 2, local: 6, arrow: 2, remote: 6, bar: 1, rate: 6, packets: 6}
	if flowLayoutWidth(layout) > width {
		return flowLayout{}, false
	}
	for extra := width - flowLayoutWidth(layout); extra > 0; extra-- {
		switch {
		case layout.bar < 8:
			layout.bar++
		case layout.local < 15:
			layout.local++
		case layout.remote < 24:
			layout.remote++
		case layout.bar < 20:
			layout.bar++
		case layout.local < 25:
			layout.local++
		case layout.remote < 64:
			layout.remote++
		default:
			extra = 0
		}
	}
	return layout, true
}

func flowLayoutWidth(layout flowLayout) int {
	return layout.rank + layout.local + layout.arrow + layout.remote +
		layout.bar + 3*layout.rate + layout.packets + 8
}

func flowLine(layout flowLayout, rank, local, arrow, remote, bar, rate2, rate10, rate40, packets string) string {
	widths := []int{layout.rank, layout.local, layout.arrow, layout.remote, layout.bar,
		layout.rate, layout.rate, layout.rate, layout.packets}
	values := []string{rank, local, arrow, remote, bar, rate2, rate10, rate40, packets}
	cells := make([]string, len(values))
	for i := range values {
		cells[i] = fitCell(values[i], widths[i], i >= 5)
	}
	return strings.Join(cells, " ")
}

func flowLineStyled(layout flowLayout, rank, local, arrow, remote, bar, rate2, rate10, rate40, packets, style string, ansi bool) string {
	if !ansi {
		return flowLine(layout, rank, local, arrow, remote, bar, rate2, rate10, rate40, packets)
	}
	widths := []int{layout.rank, layout.local, layout.arrow, layout.remote, layout.bar,
		layout.rate, layout.rate, layout.rate, layout.packets}
	values := []string{rank, local, arrow, remote, bar, rate2, rate10, rate40, packets}
	cells := make([]string, len(values))
	for i := range values {
		cells[i] = fitCell(values[i], widths[i], i >= 5)
	}
	cells[0] = color(ansiBold, cells[0], true)
	cells[2] = color(style, cells[2], true)
	cells[4] = color(style, cells[4], true)
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
	return strings.Repeat("#", filled) + strings.Repeat(".", width-filled)
}

func maximumDirectionalRate(rows []Row) float64 {
	var maximum float64
	for _, row := range rows {
		maximum = math.Max(maximum, row.Stat.Tx.Two)
		maximum = math.Max(maximum, row.Stat.Rx.Two)
	}
	return maximum
}

func remoteIdentity(row Row) string {
	return strings.TrimSpace(strings.Join(nonEmpty(row.Stat.IP,
		first(row.Info.Hostname, row.Stat.Hostname)), " "))
}

func remoteMetadata(row Row) string {
	asn := ""
	if row.Info.ASN != 0 {
		asn = fmt.Sprintf("AS%d", row.Info.ASN)
	}
	source := row.Info.Source
	if row.Info.Err != "" {
		if source == "" {
			source = "error"
		} else {
			source += "!"
		}
	}
	meta := strings.Join(nonEmpty(asn, row.Info.Provider, row.Info.Country, source), " / ")
	return strings.TrimSpace(strings.Join(nonEmpty(row.Stat.IP, meta), " "))
}

func nonEmpty(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = sanitizeTerminal(value); value != "" {
			out = append(out, value)
		}
	}
	return out
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
	width := 0
	for _, r := range s {
		width += terminalRuneWidth(r)
	}
	return width
}

func terminalRuneWidth(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) {
		return 0
	}
	switch {
	case r >= 0x1100 && r <= 0x115f,
		r >= 0x2329 && r <= 0x232a,
		r >= 0x2e80 && r <= 0xa4cf,
		r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff,
		r >= 0xfe10 && r <= 0xfe19,
		r >= 0xfe30 && r <= 0xfe6f,
		r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6,
		r >= 0x1f300 && r <= 0x1faff,
		r >= 0x20000 && r <= 0x3fffd:
		return 2
	default:
		return 1
	}
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
