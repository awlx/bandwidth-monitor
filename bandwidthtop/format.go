package bandwidthtop

import (
	"fmt"
	"strconv"
	"strings"
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
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return string(r[:1])
	}
	return string(r[:width-1]) + "~"
}

func Render(rows []Row, width int) string {
	if width <= 0 {
		width = 120
	}
	columns := []struct {
		name  string
		width int
	}{
		{"REMOTE", 15}, {"LOCAL", 15}, {"HOSTNAME", 18}, {"RX", 11}, {"TX", 11},
		{"TOTAL", 11}, {"PACKETS", 8}, {"ASN", 8}, {"PROVIDER", 18}, {"COUNTRY", 12}, {"SOURCE", 14},
	}
	if width < 48 {
		columns = []struct {
			name  string
			width int
		}{{"REMOTE", max(8, width-14)}, {"TOTAL", 11}}
	} else if width < 92 {
		columns = []struct {
			name  string
			width int
		}{{"REMOTE", 15}, {"LOCAL", 15}, {"TOTAL", 11}, {"PACKETS", 8}}
	}
	var b strings.Builder
	for _, c := range columns {
		fmt.Fprintf(&b, "%-*s ", c.width, Truncate(c.name, c.width))
	}
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
		for _, c := range columns {
			index := map[string]int{"REMOTE": 0, "LOCAL": 1, "HOSTNAME": 2, "RX": 3, "TX": 4, "TOTAL": 5, "PACKETS": 6, "ASN": 7, "PROVIDER": 8, "COUNTRY": 9, "SOURCE": 10}[c.name]
			fmt.Fprintf(&b, "%-*s ", c.width, Truncate(values[index], c.width))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
