package metrics

import (
	"bufio"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// metricType is a Prometheus metric type as written in the "# TYPE" line.
type metricType string

const (
	gaugeType   metricType = "gauge"
	counterType metricType = "counter"
)

// Label is a single name/value pair attached to a sample.
type Label struct {
	Name  string
	Value string
}

type sample struct {
	labels []Label
	value  float64
}

type family struct {
	name    string
	help    string
	typ     metricType
	samples []sample
	seen    map[string]struct{}
}

// registry collects samples for a single scrape and renders them in the
// Prometheus text exposition format (version 0.0.4). It is intentionally
// minimal so the daemon does not need the full client library: every scrape
// builds a fresh registry from the subsystems' current snapshots.
type registry struct {
	families map[string]*family
	order    []string
}

func newRegistry() *registry {
	return &registry{families: make(map[string]*family)}
}

func (r *registry) family(name, help string, typ metricType) *family {
	if f, ok := r.families[name]; ok {
		return f
	}
	f := &family{name: name, help: help, typ: typ, seen: make(map[string]struct{})}
	r.families[name] = f
	r.order = append(r.order, name)
	return f
}

// gauge adds a gauge sample. Duplicate label sets within a family are
// dropped so a scrape is always valid even if a source reports duplicates.
func (r *registry) gauge(name, help string, value float64, labels ...Label) {
	r.family(name, help, gaugeType).add(value, labels)
}

func (r *registry) counter(name, help string, value float64, labels ...Label) {
	r.family(name, help, counterType).add(value, labels)
}

func (f *family) add(value float64, labels []Label) {
	key := labelKey(labels)
	if _, dup := f.seen[key]; dup {
		return
	}
	f.seen[key] = struct{}{}
	f.samples = append(f.samples, sample{labels: labels, value: value})
}

func labelKey(labels []Label) string {
	var b strings.Builder
	for _, l := range labels {
		b.WriteString(l.Name)
		b.WriteByte(0)
		b.WriteString(l.Value)
		b.WriteByte(0)
	}
	return b.String()
}

// WriteTo renders all families sorted by name.
func (r *registry) WriteTo(w io.Writer) (int64, error) {
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	bw := bufio.NewWriter(w)
	cw := &countingWriter{w: bw}
	for _, name := range names {
		f := r.families[name]
		cw.writeString("# HELP " + f.name + " " + escapeHelp(f.help) + "\n")
		cw.writeString("# TYPE " + f.name + " " + string(f.typ) + "\n")
		for _, s := range f.samples {
			cw.writeString(f.name)
			if len(s.labels) > 0 {
				cw.writeString("{")
				for i, l := range s.labels {
					if i > 0 {
						cw.writeString(",")
					}
					cw.writeString(l.Name + `="` + escapeLabelValue(l.Value) + `"`)
				}
				cw.writeString("}")
			}
			cw.writeString(" " + formatFloat(s.value) + "\n")
		}
	}
	if cw.err != nil {
		return cw.n, cw.err
	}
	return cw.n, bw.Flush()
}

type countingWriter struct {
	w   *bufio.Writer
	n   int64
	err error
}

func (c *countingWriter) writeString(s string) {
	if c.err != nil {
		return
	}
	n, err := c.w.WriteString(s)
	c.n += int64(n)
	c.err = err
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

func escapeHelp(s string) string { return helpEscaper.Replace(s) }

// escapeLabelValue escapes a label value and replaces invalid UTF-8, which
// would otherwise make Prometheus reject the whole scrape.
func escapeLabelValue(s string) string {
	return labelEscaper.Replace(strings.ToValidUTF8(s, "\uFFFD"))
}

func formatFloat(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
