package bandwidthtop

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Stat struct {
	IP        string
	Hostname  string
	RxRate    float64
	TxRate    float64
	RateBytes float64
	Packets   uint64
}

type Row struct {
	LocalIP string
	Stat    Stat
	Info    Enrichment
}

type column struct {
	key       int
	name      string
	minWidth  int
	preferred int
	width     int
}

var allColumns = []column{
	{0, "REMOTE", 12, 15, 0},
	{1, "LOCAL", 12, 15, 0},
	{2, "HOSTNAME", 10, 18, 0},
	{3, "RX", 9, 11, 0},
	{4, "TX", 9, 11, 0},
	{5, "TOTAL", 9, 11, 0},
	{6, "PACKETS", 6, 8, 0},
	{7, "ASN", 6, 8, 0},
	{8, "PROVIDER", 10, 18, 0},
	{9, "COUNTRY", 7, 12, 0},
	{10, "SOURCE", 8, 14, 0},
}

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

func Render(rows []Row, width int) string {
	if width <= 0 {
		width = 120
	}
	columns := layout(width)
	var b strings.Builder
	b.WriteString(renderCells(columns, func(c column) string { return c.name }))
	b.WriteByte('\n')
	for _, row := range rows {
		source := row.Info.Source
		if row.Info.Err != "" {
			if source == "" {
				source = "error"
			} else {
				source += "!"
			}
		}
		values := []string{
			row.Stat.IP, row.LocalIP, first(row.Info.Hostname, row.Stat.Hostname),
			FormatRate(row.Stat.RxRate), FormatRate(row.Stat.TxRate), FormatRate(row.Stat.RateBytes),
			strconv.FormatUint(row.Stat.Packets, 10), strconv.FormatUint(uint64(row.Info.ASN), 10),
			row.Info.Provider, row.Info.Country, source,
		}
		b.WriteString(renderCells(columns, func(c column) string { return values[c.key] }))
		b.WriteByte('\n')
	}
	return b.String()
}

func layout(width int) []column {
	var selected []column
	switch {
	case width >= minimumWidth(allColumns):
		selected = append(selected, allColumns...)
	case width >= minimumWidth(allColumns[:7]):
		selected = append(selected, allColumns[:7]...)
	case width >= minimumWidth([]column{allColumns[0], allColumns[1], allColumns[5], allColumns[6]}):
		selected = append(selected, allColumns[0], allColumns[1], allColumns[5], allColumns[6])
	case width >= minimumWidth([]column{allColumns[0], allColumns[5]}):
		selected = append(selected, allColumns[0], allColumns[5])
	default:
		selected = append(selected, allColumns[0])
		selected[0].minWidth = max(1, width)
		selected[0].preferred = selected[0].minWidth
	}
	for i := range selected {
		selected[i].width = selected[i].minWidth
	}
	extra := width - minimumWidth(selected)
	for extra > 0 {
		changed := false
		for i := range selected {
			if selected[i].width < selected[i].preferred {
				selected[i].width++
				extra--
				changed = true
				if extra == 0 {
					break
				}
			}
		}
		if !changed {
			break
		}
	}
	return selected
}

func minimumWidth(columns []column) int {
	if len(columns) == 0 {
		return 0
	}
	total := len(columns) - 1
	for _, c := range columns {
		total += c.minWidth
	}
	return total
}

func renderCells(columns []column, value func(column) string) string {
	cells := make([]string, len(columns))
	for i, c := range columns {
		cell := Truncate(value(c), c.width)
		cells[i] = cell + strings.Repeat(" ", c.width-displayWidth(cell))
	}
	return strings.Join(cells, " ")
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
