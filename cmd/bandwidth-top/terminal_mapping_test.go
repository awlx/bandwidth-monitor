//go:build linux || darwin

package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"bandwidth-monitor/bandwidthtop"
	"bandwidth-monitor/talkers"
	"bandwidth-monitor/version"

	tea "charm.land/bubbletea/v2"
	"github.com/rivo/uniseg"
)

type mappingTestTracker struct {
	rows  []talkers.DirectTalkerStat
	modes []talkers.DirectViewMode
	errs  chan error
}

func (tracker *mappingTestTracker) DirectBandwidthSnapshotForMode(
	mode talkers.DirectViewMode,
	_ int,
) ([]talkers.DirectTalkerStat, talkers.DirectRateTotals) {
	tracker.modes = append(tracker.modes, mode)
	return tracker.rows, talkers.DirectRateTotals{}
}

func (tracker *mappingTestTracker) Errors() <-chan error {
	return tracker.errs
}

type mappingTestEnricher struct{}

func (mappingTestEnricher) Lookup(string) bandwidthtop.Enrichment {
	return bandwidthtop.Enrichment{}
}

func (mappingTestEnricher) SourceStatusLines(int) []string {
	return nil
}

type mappingTestResolver struct {
	enabled bool
	names   map[string]string
	lookups map[string]int
	states  []bool
}

func (resolver *mappingTestResolver) SetEnabled(enabled bool) {
	resolver.enabled = enabled
	resolver.states = append(resolver.states, enabled)
}

func (resolver *mappingTestResolver) LookupAddrAsync(ip string) string {
	resolver.lookups[ip]++
	if !resolver.enabled {
		return ip
	}
	if name := resolver.names[ip]; name != "" {
		return name
	}
	return ip
}

func TestLocalPTRRuntimeToggleCoversHostAndPortViews(t *testing.T) {
	tracker := &mappingTestTracker{
		rows: []talkers.DirectTalkerStat{{
			LocalIP: "192.0.2.10",
			TalkerStat: talkers.TalkerStat{
				IP: "198.51.100.20", Hostname: "remote.example",
			},
			Protocol: "TCP", RemotePort: 443, HasPort: true,
		}},
		errs: make(chan error),
	}
	resolver := &mappingTestResolver{
		enabled: true,
		names:   map[string]string{"192.0.2.10": "local.example"},
		lookups: make(map[string]int),
	}
	model := newLiveModel(liveModelConfig{
		title: "bandwidth-top", rows: 20, refresh: time.Second,
		tracker: tracker, enricher: mappingTestEnricher{}, resolver: resolver,
		done: make(chan struct{}), initialSize: terminalDimensions{width: 120, height: 24},
	})

	updateMappingModel(t, model, tickMsg(time.Now()))
	assertMappingView(t, model, "local.example", "remote.example")

	updateMappingModel(t, model, mappingKeyPress("p"))
	if model.mode != bandwidthtop.ViewPorts ||
		tracker.modes[len(tracker.modes)-1] != talkers.DirectViewPorts {
		t.Fatalf("mode=%v snapshots=%v", model.mode, tracker.modes)
	}
	assertMappingView(t, model, "local.example", "remote.example")

	updateMappingModel(t, model, mappingKeyPress("n"))
	if !model.noResolve || len(resolver.states) != 1 || resolver.states[0] {
		t.Fatalf("noResolve=%v states=%v", model.noResolve, resolver.states)
	}
	assertMappingView(t, model, "192.0.2.10", "198.51.100.20")

	updateMappingModel(t, model, mappingKeyPress("n"))
	if model.noResolve || len(resolver.states) != 2 || !resolver.states[1] {
		t.Fatalf("noResolve=%v states=%v", model.noResolve, resolver.states)
	}
	assertMappingView(t, model, "local.example", "remote.example")
}

func TestLocalPTRLookupsAreDeduplicatedPerSnapshot(t *testing.T) {
	stats := []talkers.DirectTalkerStat{
		{
			LocalIP:    "192.0.2.10",
			TalkerStat: talkers.TalkerStat{IP: "198.51.100.20"},
		},
		{
			LocalIP:    "192.0.2.10",
			TalkerStat: talkers.TalkerStat{IP: "203.0.113.20"},
		},
	}

	resolver := &mappingTestResolver{
		enabled: true,
		names:   map[string]string{"192.0.2.10": "local.example"},
		lookups: make(map[string]int),
	}
	rows, _ := mapSnapshotRows(
		stats, talkers.DirectRateTotals{}, mappingTestEnricher{},
		resolver, false, bandwidthtop.ViewHosts,
	)
	if len(rows) != 2 || rows[0].LocalHostname != "local.example" ||
		rows[1].LocalHostname != "local.example" ||
		resolver.lookups["192.0.2.10"] != 1 {
		t.Fatalf("rows=%+v lookups=%v", rows, resolver.lookups)
	}

	resolver.lookups = make(map[string]int)
	rows, _ = mapSnapshotRows(
		stats, talkers.DirectRateTotals{}, mappingTestEnricher{},
		resolver, true, bandwidthtop.ViewPorts,
	)
	if len(rows) != 2 || rows[0].LocalHostname != "" ||
		rows[1].LocalHostname != "" || len(resolver.lookups) != 0 {
		t.Fatalf("no-resolve rows=%+v lookups=%v", rows, resolver.lookups)
	}
}

func TestLiveHelpScreenContentAndCloseKeys(t *testing.T) {
	originalVersion, originalCommit := version.Version, version.Commit
	t.Cleanup(func() { version.Version, version.Commit = originalVersion, originalCommit })
	version.Version, version.Commit = "7.8.9", "ignored"

	model := newLiveModel(liveModelConfig{
		title: "bandwidth-top", rows: 20, refresh: time.Second,
		tracker:  &mappingTestTracker{errs: make(chan error)},
		enricher: mappingTestEnricher{}, resolver: &mappingTestResolver{},
		done: make(chan struct{}), initialSize: terminalDimensions{width: 120, height: 24},
		initialMode: bandwidthtop.ViewPorts,
	})
	status := model.View().Content
	if !strings.Contains(status, "p mode | n rDNS | ? help | q quit") ||
		strings.Contains(status, "h/? help") {
		t.Fatalf("normal status is not concise:\n%s", status)
	}
	updateMappingModel(t, model, mappingKeyPress("?"))
	help := model.View().Content
	for _, want := range []string{
		"bandwidth-top 7.8.9 - help",
		"Current: view: ports | rdns: on",
		"hosts group by remote IP",
		"outbound destination, inbound source",
		"LOCAL => REMOTE is TX",
		"2s, 10s, and 40s",
		"largest directional 2s rate",
		"SINCE START",
		"local MMDB -> monitor -> public fallback",
		"both endpoints",
		"raw IPs remain the fallback",
		"-i interface", "-L rows", "-t snapshot", "-P ports",
		"-n no-resolve", "-v version",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
	if strings.Contains(help, "# LOCAL") {
		t.Fatalf("help did not replace the traffic view:\n%s", help)
	}

	updateMappingModel(t, model, mappingKeyPress("p"))
	updateMappingModel(t, model, mappingKeyPress("n"))
	help = model.View().Content
	if !strings.Contains(help, "Current: view: hosts | rdns: off") {
		t.Fatalf("help did not reflect live mode and PTR toggles:\n%s", help)
	}

	for _, message := range []tea.Msg{
		mappingKeyPress("h"),
		mappingKeyPress("?"),
		tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}),
	} {
		model.showHelp = true
		updateMappingModel(t, model, message)
		if model.showHelp {
			t.Fatalf("%v did not close help", message)
		}
	}
}

func TestLiveHelpScreenRespectsTerminalDimensions(t *testing.T) {
	for _, size := range []terminalDimensions{
		{width: 1, height: 1},
		{width: 20, height: 2},
		{width: 55, height: 8},
		{width: 80, height: 12},
		{width: 120, height: 24},
	} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			model := newLiveModel(liveModelConfig{
				title: "bandwidth-top", rows: 20, refresh: time.Second,
				tracker:  &mappingTestTracker{errs: make(chan error)},
				enricher: mappingTestEnricher{}, resolver: &mappingTestResolver{},
				done: make(chan struct{}), initialSize: size,
			})
			model.showHelp = true
			view := model.View()
			lines := strings.Split(view.Content, "\n")
			if len(lines) > size.height {
				t.Fatalf("rendered %d lines for height %d:\n%s", len(lines), size.height, view.Content)
			}
			width := liveDimensions(size, 0).width
			for _, line := range lines {
				if got := uniseg.StringWidth(line); got > width {
					t.Fatalf("line width=%d exceeds %d: %q", got, width, line)
				}
			}
		})
	}
}

func updateMappingModel(t *testing.T, model *liveModel, message any) {
	t.Helper()
	updated, _ := model.Update(message)
	if updated != model {
		t.Fatalf("update returned %T", updated)
	}
}

func mappingKeyPress(key string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: key, Code: []rune(key)[0]})
}

func assertMappingView(t *testing.T, model *liveModel, local, remote string) {
	t.Helper()
	view := model.View().Content
	if !strings.Contains(view, local) || !strings.Contains(view, remote) {
		t.Fatalf("view missing local=%q remote=%q:\n%s", local, remote, view)
	}
}
