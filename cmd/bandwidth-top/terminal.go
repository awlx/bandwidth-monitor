//go:build linux || darwin

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"bandwidth-monitor/bandwidthtop"
	"bandwidth-monitor/talkers"
	"bandwidth-monitor/version"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/sys/unix"
)

const (
	defaultWidth  = 120
	defaultHeight = 24
)

type terminalDimensions struct {
	width  int
	height int
}

type liveTracker interface {
	DirectBandwidthSnapshotForMode(talkers.DirectViewMode, int) ([]talkers.DirectTalkerStat, talkers.DirectRateTotals)
	Errors() <-chan error
}

type liveEnricher interface {
	Lookup(string) bandwidthtop.Enrichment
	SourceStatusLines(int) []string
}

type resolverControl interface {
	SetEnabled(bool)
	LookupAddrAsync(string) string
}

type liveModelConfig struct {
	title       string
	rows        int
	refresh     time.Duration
	width       int
	noResolve   bool
	tracker     liveTracker
	enricher    liveEnricher
	resolver    resolverControl
	done        <-chan struct{}
	initialSize terminalDimensions
	initialMode bandwidthtop.ViewMode
}

type liveModel struct {
	config    liveModelConfig
	size      terminalDimensions
	rows      []bandwidthtop.Row
	totals    bandwidthtop.Totals
	noResolve bool
	mode      bandwidthtop.ViewMode
	showHelp  bool
	err       error
	ticks     int
}

type tickMsg time.Time

type captureErrorMsg struct {
	err error
}

func newLiveModel(config liveModelConfig) *liveModel {
	size := config.initialSize
	if size.width <= 0 {
		size.width = defaultWidth
	}
	if size.height <= 0 {
		size.height = defaultHeight
	}
	return &liveModel{
		config: config, size: size, noResolve: config.noResolve, mode: config.initialMode,
	}
}

func (m *liveModel) Init() tea.Cmd {
	return tea.Batch(
		tickAfter(m.config.refresh),
		waitForCaptureError(m.config.tracker.Errors(), m.config.done),
	)
}

func (m *liveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg.Keystroke())
	case tea.WindowSizeMsg:
		m.size = terminalDimensions{width: msg.Width, height: msg.Height}
	case tickMsg:
		m.refreshSnapshot()
		m.ticks++
		return m, tickAfter(m.config.refresh)
	case captureErrorMsg:
		m.err = captureError(msg.err)
		return m, tea.Quit
	}
	return m, nil
}

func (m *liveModel) handleKey(key string) (tea.Model, tea.Cmd) {
	if m.showHelp {
		switch key {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "h", "?", "esc":
			m.showHelp = false
			return m, nil
		case "p", "n":
			// Keep advertised mode and PTR controls active while help is open.
		default:
			return m, nil
		}
	}
	switch key {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "n":
		m.noResolve = !m.noResolve
		m.config.resolver.SetEnabled(!m.noResolve)
		m.refreshSnapshot()
	case "p":
		if m.mode == bandwidthtop.ViewPorts {
			m.mode = bandwidthtop.ViewHosts
		} else {
			m.mode = bandwidthtop.ViewPorts
		}
		m.refreshSnapshot()
	case "h", "?":
		m.showHelp = !m.showHelp
	}
	return m, nil
}

func (m *liveModel) refreshSnapshot() {
	m.rows, m.totals = snapshotRowsForMode(
		m.config.tracker, m.config.enricher, m.config.resolver,
		m.config.rows, m.noResolve, m.mode)
}

func (m *liveModel) View() tea.View {
	size := liveDimensions(m.size, m.config.width)
	if m.showHelp {
		view := tea.NewView(renderHelpScreen(size, m.mode, !m.noResolve))
		view.AltScreen = true
		return view
	}
	status := []string{viewStatus(m.mode) + " | " + rdnsStatus(!m.noResolve) + " | p mode | n rDNS | ? help | q quit"}
	status = append(status, m.config.enricher.SourceStatusLines(size.width)...)
	if lookupStatus := lookupErrorStatus(m.rows); lookupStatus != "" {
		status = append(status, lookupStatus)
	}
	rows := make([]bandwidthtop.Row, len(m.rows))
	copy(rows, m.rows)
	for i := range rows {
		rows[i].NoResolve = m.noResolve
	}
	view := tea.NewView(composeFrameForMode(
		m.config.title, status, rows, m.totals, m.config.rows, size, true, m.mode))
	view.AltScreen = true
	return view
}

func renderHelpScreen(size terminalDimensions, mode bandwidthtop.ViewMode, resolve bool) string {
	width, height := size.width, size.height
	if width <= 0 {
		width = defaultWidth
	}
	if height <= 0 {
		height = defaultHeight
	}
	current := fmt.Sprintf("Current: %s | %s", viewStatus(mode), rdnsStatus(resolve))
	lines := []string{
		fmt.Sprintf("bandwidth-top %s - help", version.String()),
		current,
		"",
		"Modes: hosts group by remote IP; ports group by remote IP, port, and protocol.",
		`Remote port: outbound destination, inbound source; "-" means unavailable.`,
		"Directions: LOCAL => REMOTE is TX; LOCAL <= REMOTE is RX.",
		"Rates: 2s, 10s, and 40s are rolling bit/s averages.",
		"Graph: bars scale to the largest directional 2s rate currently shown.",
		"SINCE START: cumulative TX/RX bytes; changing views does not reset totals.",
		"Enrichment: local MMDB -> monitor -> public fallback, for remote peers only.",
		"PTR: one shared async cache resolves both endpoints; raw IPs remain the fallback.",
		"",
		"Keys: p host/port mode | n PTR on/off",
		"      h / ? / Esc close help | q / Ctrl-C quit",
		"",
		"CLI: -i interface | -L rows",
		"     -t snapshot | -P ports",
		"     -n no-resolve | -v version",
		"",
		"Press h, ?, or Esc to return.",
	}
	if width < 72 {
		lines = []string{
			fmt.Sprintf("bandwidth-top %s - help", version.String()),
			current,
			"",
			"Modes: hosts=remote IP; ports=remote IP+port/protocol.",
			`Port: outbound dst; inbound src; "-" if unavailable.`,
			"Arrows: => TX local-to-remote; <= RX remote-to-local.",
			"Rates: rolling 2s / 10s / 40s bit/s averages.",
			"Graph: scaled to the largest shown directional 2s rate.",
			"SINCE START: cumulative TX/RX; view changes keep totals.",
			"Enrich: local MMDB -> monitor -> public; remote only.",
			"PTR: shared async cache for both ends; raw IP fallback.",
			"",
			"Keys: p mode | n PTR | h/?/Esc back | q/Ctrl-C quit",
			"CLI: -i iface | -L rows | -t snapshot | -P ports",
			"     -n no-resolve | -v version",
			"",
			"Press h, ?, or Esc to return.",
		}
	}
	lines = boundedLines(lines, width)
	if len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	if height == 1 {
		return lines[0]
	}
	visible := append([]string(nil), lines[:height-1]...)
	visible = append(visible, bandwidthtop.Truncate("Press h, ?, or Esc to return.", width))
	return strings.Join(visible, "\n")
}

func tickAfter(delay time.Duration) tea.Cmd {
	return tea.Tick(delay, func(now time.Time) tea.Msg { return tickMsg(now) })
}

func waitForCaptureError(errors <-chan error, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		select {
		case err, ok := <-errors:
			if !ok {
				return nil
			}
			return captureErrorMsg{err: err}
		case <-done:
			return nil
		}
	}
}

func snapshotRows(
	tracker interface {
		DirectBandwidthSnapshot(int) ([]talkers.DirectTalkerStat, talkers.DirectRateTotals)
	},
	enricher interface {
		Lookup(string) bandwidthtop.Enrichment
	},
	resolver resolverControl,
	rowLimit int,
	noResolve bool,
) ([]bandwidthtop.Row, bandwidthtop.Totals) {
	stats, rateTotals := tracker.DirectBandwidthSnapshot(rowLimit)
	return mapSnapshotRows(
		stats, rateTotals, enricher, resolver, noResolve, bandwidthtop.ViewHosts)
}

func snapshotRowsForMode(
	tracker interface {
		DirectBandwidthSnapshotForMode(talkers.DirectViewMode, int) ([]talkers.DirectTalkerStat, talkers.DirectRateTotals)
	},
	enricher interface {
		Lookup(string) bandwidthtop.Enrichment
	},
	resolver resolverControl,
	rowLimit int,
	noResolve bool,
	mode bandwidthtop.ViewMode,
) ([]bandwidthtop.Row, bandwidthtop.Totals) {
	directMode := talkers.DirectViewHosts
	if mode == bandwidthtop.ViewPorts {
		directMode = talkers.DirectViewPorts
	}
	stats, rateTotals := tracker.DirectBandwidthSnapshotForMode(directMode, rowLimit)
	return mapSnapshotRows(stats, rateTotals, enricher, resolver, noResolve, mode)
}

func mapSnapshotRows(
	stats []talkers.DirectTalkerStat,
	rateTotals talkers.DirectRateTotals,
	enricher interface {
		Lookup(string) bandwidthtop.Enrichment
	},
	resolver resolverControl,
	noResolve bool,
	mode bandwidthtop.ViewMode,
) ([]bandwidthtop.Row, bandwidthtop.Totals) {
	rows := make([]bandwidthtop.Row, 0, len(stats))
	enrichmentByIP := make(map[string]bandwidthtop.Enrichment, len(stats))
	localHostnameByIP := make(map[string]string)
	for _, stat := range stats {
		info, ok := enrichmentByIP[stat.IP]
		if !ok {
			info = enricher.Lookup(stat.IP)
			enrichmentByIP[stat.IP] = info
		}
		localHostname := ""
		if !noResolve && resolver != nil {
			var ok bool
			localHostname, ok = localHostnameByIP[stat.LocalIP]
			if !ok {
				localHostname = resolver.LookupAddrAsync(stat.LocalIP)
				localHostnameByIP[stat.LocalIP] = localHostname
			}
		}
		row := bandwidthtop.Row{
			LocalIP:       stat.LocalIP,
			LocalHostname: localHostname,
			NoResolve:     noResolve,
			Stat: bandwidthtop.Stat{
				IP: stat.IP, Hostname: stat.Hostname, Packets: stat.Packets,
				Rx: bandwidthtop.RateWindows{
					Two: stat.RxRate, Ten: stat.RxRate10, Forty: stat.RxRate40,
				},
				Tx: bandwidthtop.RateWindows{
					Two: stat.TxRate, Ten: stat.TxRate10, Forty: stat.TxRate40,
				},
			},
			Info: info,
		}
		if mode == bandwidthtop.ViewPorts {
			row.Protocol = stat.Protocol
			row.Port = "-"
			if stat.HasPort {
				row.Port = strconv.FormatUint(uint64(stat.RemotePort), 10)
			}
		}
		rows = append(rows, row)
	}

	return rows, bandwidthtop.Totals{
		Rx: bandwidthtop.RateWindows{
			Two: rateTotals.RxRate, Ten: rateTotals.RxRate10, Forty: rateTotals.RxRate40,
		},
		Tx: bandwidthtop.RateWindows{
			Two: rateTotals.TxRate, Ten: rateTotals.TxRate10, Forty: rateTotals.TxRate40,
		},
		RxBytes:       rateTotals.RxBytes,
		TxBytes:       rateTotals.TxBytes,
		HasCumulative: true,
	}
}

func viewStatus(mode bandwidthtop.ViewMode) string {
	if mode == bandwidthtop.ViewPorts {
		return "view: ports"
	}
	return "view: hosts"
}

func captureError(err error) error {
	if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
		return fmt.Errorf("capture failed: %w (%s)", err, capturePrivilegeHint())
	}
	return fmt.Errorf("capture failed: %w", err)
}

func rdnsStatus(enabled bool) string {
	if enabled {
		return "rdns: on"
	}
	return "rdns: off"
}

func liveDimensions(size terminalDimensions, configuredWidth int) terminalDimensions {
	if size.width <= 0 {
		size.width = defaultWidth
	}
	if size.height <= 0 {
		size.height = defaultHeight
	}
	if configuredWidth > 0 && configuredWidth < size.width {
		size.width = configuredWidth
	}
	if size.width > 1 {
		size.width--
	}
	return size
}

func snapshotDimensions(out io.Writer, configuredWidth int) terminalDimensions {
	size := terminalSize(out)
	if configuredWidth > 0 {
		size.width = configuredWidth
	}
	return size
}

func composeFrame(title string, status []string, rows []bandwidthtop.Row, totals bandwidthtop.Totals, maxRows int, size terminalDimensions, ansi bool) string {
	return composeFrameForMode(title, status, rows, totals, maxRows, size, ansi, bandwidthtop.ViewHosts)
}

func composeFrameForMode(title string, status []string, rows []bandwidthtop.Row, totals bandwidthtop.Totals, maxRows int, size terminalDimensions, ansi bool, mode bandwidthtop.ViewMode) string {
	width := size.width
	if width <= 0 {
		width = defaultWidth
	}
	title = bandwidthtop.Truncate(title, width)
	if !ansi || size.height <= 0 {
		lines := append([]string{title}, boundedLines(status, width)...)
		lines = append(lines, splitFrame(bandwidthtop.RenderSnapshotForMode(rows, totals, width, maxRows, mode))...)
		return strings.Join(lines, "\n")
	}

	height := size.height
	status = boundedLines(status, width)
	minimumBody := 7
	if totals.HasCumulative {
		minimumBody++
	}
	if len(rows) > 0 {
		minimumBody += 2
	}
	for len(status) > 1 && 1+len(status)+minimumBody > height {
		status = status[:len(status)-1]
	}
	if 1+len(status)+8 <= height {
		pairLimit := maxInt(0, (height-1-len(status)-8)/2)
		if len(rows) == 0 || pairLimit > 0 {
			if len(rows) > pairLimit {
				rows = rows[:pairLimit]
			}
			lines := append([]string{title}, status...)
			lines = append(lines, splitFrame(bandwidthtop.RenderLiveForMode(rows, totals, width, maxRows, mode))...)
			return strings.Join(lines, "\n")
		}
	}
	return composeShortFrameForMode(title, status, rows, totals, maxRows, width, height, mode)
}

// Very short terminals preserve title and since-start totals first. Rate totals,
// column identity, and complete pairs are added as space permits.
func composeShortFrame(title string, status []string, rows []bandwidthtop.Row, totals bandwidthtop.Totals, maxRows, width, height int) string {
	return composeShortFrameForMode(title, status, rows, totals, maxRows, width, height, bandwidthtop.ViewHosts)
}

func composeShortFrameForMode(title string, status []string, rows []bandwidthtop.Row, totals bandwidthtop.Totals, maxRows, width, height int, mode bandwidthtop.ViewMode) string {
	if height <= 1 {
		return bandwidthtop.Truncate(title, width)
	}
	rendered := splitFrame(bandwidthtop.RenderLiveForMode(rows, totals, width, maxRows, mode))
	headerIndex := lineContaining(rendered, "LOCAL")
	header := ""
	total := ""
	sinceStart := ""
	if headerIndex >= 0 {
		header = rendered[headerIndex]
	}
	footerLines := 5
	if totals.HasCumulative {
		footerLines++
	}
	footerStart := len(rendered) - footerLines
	if footerStart >= 0 {
		total = rendered[footerStart+3]
		if totals.HasCumulative {
			sinceStart = rendered[footerStart+4]
		}
	}
	lines := []string{title}
	remaining := height - 1
	if remaining == 1 {
		if sinceStart != "" {
			lines = append(lines, sinceStart)
		} else if total != "" {
			lines = append(lines, total)
		}
		return strings.Join(lines, "\n")
	}
	if len(status) > 0 && remaining >= 4 {
		lines = append(lines, status[0])
		remaining--
	}
	if header != "" && remaining >= 3 {
		lines = append(lines, header)
		remaining--
	}
	pairStart := headerIndex + 1
	for pair := 0; pair < len(rows) && pairStart >= 1 &&
		pairStart+1 < len(rendered) && remaining >= 4; pair++ {
		lines = append(lines, rendered[pairStart], rendered[pairStart+1])
		pairStart += 2
		remaining -= 2
	}
	for offset := 1; offset <= 2; offset++ {
		if remaining <= 2 {
			break
		}
		if footerStart >= 0 {
			lines = append(lines, rendered[footerStart+offset])
			remaining--
		}
	}
	if remaining > 0 && total != "" {
		lines = append(lines, total)
		remaining--
	}
	if remaining > 0 && sinceStart != "" {
		lines = append(lines, sinceStart)
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func boundedLines(lines []string, width int) []string {
	bounded := make([]string, len(lines))
	for i, line := range lines {
		bounded[i] = bandwidthtop.Truncate(line, width)
	}
	return bounded
}

func splitFrame(frame string) []string {
	return strings.Split(strings.TrimRight(frame, "\n"), "\n")
}

func lineContaining(lines []string, value string) int {
	for i, line := range lines {
		if strings.Contains(line, value) {
			return i
		}
	}
	return -1
}

func supportsLiveTerminal(out io.Writer, snapshot bool, term string) bool {
	if snapshot || term == "" || term == "dumb" {
		return false
	}
	output, ok := out.(*os.File)
	if !ok {
		return false
	}
	return terminalFDIsTTY(int(output.Fd()))
}

func terminalSize(out io.Writer) terminalDimensions {
	file, ok := out.(*os.File)
	if !ok {
		return terminalDimensions{defaultWidth, defaultHeight}
	}
	width, height, err := terminalFDSize(int(file.Fd()))
	if err != nil || width == 0 || height == 0 {
		return terminalDimensions{defaultWidth, defaultHeight}
	}
	return terminalDimensions{width: width, height: height}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
