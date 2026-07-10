//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"bandwidth-monitor/bandwidthtop"
	"bandwidth-monitor/talkers"

	tea "charm.land/bubbletea/v2"
	"github.com/rivo/uniseg"
	"golang.org/x/sys/unix"
)

type fakeLiveTracker struct {
	errors    chan error
	snapshots int
	rows      []talkers.DirectTalkerStat
	totals    talkers.DirectRateTotals
	modes     []talkers.DirectViewMode
}

func (t *fakeLiveTracker) DirectBandwidthSnapshot(int) ([]talkers.DirectTalkerStat, talkers.DirectRateTotals) {
	t.snapshots++
	return t.rows, t.totals
}

func (t *fakeLiveTracker) DirectBandwidthSnapshotForMode(mode talkers.DirectViewMode, _ int) ([]talkers.DirectTalkerStat, talkers.DirectRateTotals) {
	t.snapshots++
	t.modes = append(t.modes, mode)
	return t.rows, t.totals
}

func (t *fakeLiveTracker) Errors() <-chan error {
	return t.errors
}

type fakeLiveEnricher struct {
	info    bandwidthtop.Enrichment
	lookups map[string]int
}

func (e *fakeLiveEnricher) Lookup(ip string) bandwidthtop.Enrichment {
	if e.lookups != nil {
		e.lookups[ip]++
	}
	return e.info
}

func (e *fakeLiveEnricher) SourceStatusLines(int) []string {
	return []string{"enrichment: ready"}
}

type fakeResolverControl struct {
	states []bool
}

func (r *fakeResolverControl) SetEnabled(enabled bool) {
	r.states = append(r.states, enabled)
}

func TestLiveModelKeysToggleDNSHelpAndQuit(t *testing.T) {
	model, resolver := testLiveModel(false)
	model.rows = terminalTestRows(1)
	model.rows[0].Stat.Hostname = "cached.example"

	updateModel(t, model, keyPress("n"))
	if !model.noResolve || len(resolver.states) != 1 || resolver.states[0] {
		t.Fatalf("DNS toggle state=%v calls=%v", model.noResolve, resolver.states)
	}
	if view := stripTerminalANSI(model.View().Content); !strings.Contains(view, "rdns: off") ||
		!strings.Contains(view, "198.51.100.20") || strings.Contains(view, "cached.example") {
		t.Fatalf("disabled DNS view is wrong:\n%s", view)
	}

	updateModel(t, model, keyPress("n"))
	if model.noResolve || len(resolver.states) != 2 || !resolver.states[1] {
		t.Fatalf("DNS re-enable state=%v calls=%v", model.noResolve, resolver.states)
	}
	if view := stripTerminalANSI(model.View().Content); !strings.Contains(view, "rdns: on") ||
		!strings.Contains(view, "cached.example") {
		t.Fatalf("enabled DNS view is wrong:\n%s", view)
	}

	for _, key := range []string{"h", "?"} {
		updateModel(t, model, keyPress(key))
		if model.showHelp != (key == "h") {
			t.Fatalf("%s produced help=%v", key, model.showHelp)
		}
	}
	for _, msg := range []tea.Msg{keyPress("q"), tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl})} {
		_, cmd := model.Update(msg)
		if cmd == nil {
			t.Fatalf("%v did not quit", msg)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Fatalf("%v returned %T, want QuitMsg", msg, cmd())
		}
	}
}

func TestLiveModelPortModeTransitionsStatusHelpAndDNS(t *testing.T) {
	model, resolver := testLiveModel(false)
	tracker := model.config.tracker.(*fakeLiveTracker)
	tracker.rows = []talkers.DirectTalkerStat{
		{
			LocalIP: "192.0.2.10",
			TalkerStat: talkers.TalkerStat{
				IP: "198.51.100.20", Hostname: "cached.example",
			},
			Protocol: "TCP", RemotePort: 443, HasPort: true,
		},
		{
			LocalIP: "192.0.2.10",
			TalkerStat: talkers.TalkerStat{
				IP: "198.51.100.20", Hostname: "cached.example",
			},
			Protocol: "UDP", RemotePort: 443, HasPort: true,
		},
	}
	enricher := &fakeLiveEnricher{
		info:    bandwidthtop.Enrichment{ASN: 64500, Provider: "Example Networks"},
		lookups: make(map[string]int),
	}
	model.config.enricher = enricher

	updateModel(t, model, tickMsg(time.Now()))
	if model.mode != bandwidthtop.ViewHosts || tracker.modes[0] != talkers.DirectViewHosts {
		t.Fatalf("default mode=%v snapshots=%v", model.mode, tracker.modes)
	}
	updateModel(t, model, keyPress("p"))
	updateModel(t, model, keyPress("n"))
	updateModel(t, model, tickMsg(time.Now()))
	if model.mode != bandwidthtop.ViewPorts || !model.noResolve ||
		tracker.modes[1] != talkers.DirectViewPorts ||
		len(resolver.states) != 1 || resolver.states[0] {
		t.Fatalf("port+DNS mode=%v noResolve=%v snapshots=%v resolver=%v",
			model.mode, model.noResolve, tracker.modes, resolver.states)
	}
	portView := stripTerminalANSI(model.View().Content)
	for _, want := range []string{"view: ports", "rdns: off", "PORT", "PROTO", "443", "TCP"} {
		if !strings.Contains(portView, want) {
			t.Fatalf("port view missing %q:\n%s", want, portView)
		}
	}
	updateModel(t, model, keyPress("?"))
	help := stripTerminalANSI(model.View().Content)
	for _, want := range []string{
		"p toggle ports", "n toggle rDNS", "q quit", "h/? close help",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
	updateModel(t, model, keyPress("p"))
	updateModel(t, model, tickMsg(time.Now()))
	if model.mode != bandwidthtop.ViewHosts || tracker.modes[2] != talkers.DirectViewHosts {
		t.Fatalf("return mode=%v snapshots=%v", model.mode, tracker.modes)
	}
	if got := enricher.lookups["198.51.100.20"]; got != 3 {
		t.Fatalf("duplicate enrichment within port rows: lookups=%d", got)
	}
}

func TestLiveModelStartsInPortsWithNoResolve(t *testing.T) {
	model, resolver := testLiveModel(true)
	model = newLiveModel(liveModelConfig{
		title: "bandwidth-top", rows: 20, refresh: time.Second,
		noResolve: true, initialMode: bandwidthtop.ViewPorts,
		tracker: model.config.tracker, enricher: model.config.enricher,
		resolver: resolver, initialSize: terminalDimensions{width: 120, height: 24},
		done: make(chan struct{}),
	})
	updateModel(t, model, tickMsg(time.Now()))
	tracker := model.config.tracker.(*fakeLiveTracker)
	if model.mode != bandwidthtop.ViewPorts || !model.noResolve ||
		len(tracker.modes) != 1 || tracker.modes[0] != talkers.DirectViewPorts ||
		len(resolver.states) != 0 {
		t.Fatalf("startup mode=%v noResolve=%v snapshots=%v resolver=%v",
			model.mode, model.noResolve, tracker.modes, resolver.states)
	}
}

func TestLiveModelStartupNoResolveWindowAndTicks(t *testing.T) {
	model, resolver := testLiveModel(true)
	if !model.noResolve || len(resolver.states) != 0 {
		t.Fatalf("startup no-resolve state=%v calls=%v", model.noResolve, resolver.states)
	}
	updateModel(t, model, tea.WindowSizeMsg{Width: 81, Height: 12})
	if model.size != (terminalDimensions{width: 81, height: 12}) {
		t.Fatalf("window size not applied: %+v", model.size)
	}
	_, cmd := model.Update(tickMsg(time.Now()))
	if cmd == nil || model.ticks != 1 || model.config.tracker.(*fakeLiveTracker).snapshots != 1 {
		t.Fatalf("tick state ticks=%d snapshots=%d cmd=%v",
			model.ticks, model.config.tracker.(*fakeLiveTracker).snapshots, cmd)
	}
	view := model.View()
	if !view.AltScreen || !strings.Contains(stripTerminalANSI(view.Content), "rdns: off") {
		t.Fatalf("startup view is not declarative alternate screen with DNS off: %+v", view)
	}
}

func TestLiveModelCaptureErrorQuitsWithoutInterfaceDetails(t *testing.T) {
	model, _ := testLiveModel(false)
	sentinel := errors.New("permission denied")
	_, cmd := model.Update(captureErrorMsg{err: sentinel})
	if cmd == nil || !errors.Is(model.err, sentinel) ||
		strings.Contains(model.err.Error(), "eth0") || strings.Contains(model.err.Error(), "192.0.2.1") {
		t.Fatalf("capture shutdown err=%v cmd=%v", model.err, cmd)
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("capture error returned %T, want QuitMsg", cmd())
	}
}

func TestWaitForCaptureErrorCommand(t *testing.T) {
	errs := make(chan error, 1)
	done := make(chan struct{})
	sentinel := errors.New("capture stopped")
	errs <- sentinel
	msg := waitForCaptureError(errs, done)()
	got, ok := msg.(captureErrorMsg)
	if !ok || !errors.Is(got.err, sentinel) {
		t.Fatalf("got %#v", msg)
	}

	close(done)
	if msg := waitForCaptureError(make(chan error), done)(); msg != nil {
		t.Fatalf("cancelled watcher returned %#v", msg)
	}
}

func TestBubbleTeaPTYRestoresAfterQuitAndCaptureError(t *testing.T) {
	for _, test := range []struct {
		name string
		exit func(*tea.Program, *fakeLiveTracker, *os.File)
	}{
		{
			name: "q",
			exit: func(_ *tea.Program, _ *fakeLiveTracker, master *os.File) {
				_, _ = master.WriteString("q")
			},
		},
		{
			name: "capture error",
			exit: func(_ *tea.Program, tracker *fakeLiveTracker, _ *os.File) {
				tracker.errors <- errors.New("capture stopped")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			master, slave := openTestPTY(t)
			tracker := &fakeLiveTracker{errors: make(chan error, 1)}
			model := newLiveModel(liveModelConfig{
				title: "bandwidth-top", rows: 20, refresh: time.Hour,
				tracker: tracker, enricher: &fakeLiveEnricher{},
				resolver: &fakeResolverControl{},
				done:     make(chan struct{}),
			})
			program := tea.NewProgram(model,
				tea.WithInput(slave),
				tea.WithOutput(slave),
				tea.WithEnvironment([]string{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"}),
				tea.WithoutSignals(),
			)
			result := runPTYProgram(t, program, master, slave, func() {
				test.exit(program, tracker, master)
			})
			assertAlternateScreenRestored(t, result.output)
			if test.name == "capture error" && model.err == nil {
				t.Fatal("capture error was not retained by the final model")
			}
			if result.err != nil {
				t.Fatalf("program err=%v model err=%v", result.err, model.err)
			}
		})
	}
}

type panicMsg struct{}

type panicModel struct {
	panic bool
}

func (m *panicModel) Init() tea.Cmd {
	return nil
}

func (m *panicModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(panicMsg); ok {
		m.panic = true
	}
	return m, nil
}

func (m *panicModel) View() tea.View {
	if m.panic {
		panic("view failed")
	}
	view := tea.NewView("ready")
	view.AltScreen = true
	return view
}

func TestBubbleTeaPTYRestoresAfterViewPanic(t *testing.T) {
	master, slave := openTestPTY(t)
	program := tea.NewProgram(&panicModel{},
		tea.WithInput(nil),
		tea.WithOutput(slave),
		tea.WithEnvironment([]string{"TERM=xterm-256color", "TERM_PROGRAM=Apple_Terminal"}),
		tea.WithoutSignals(),
	)
	result := runPTYProgram(t, program, master, slave, func() { program.Send(panicMsg{}) })
	if !errors.Is(result.err, tea.ErrProgramPanic) {
		t.Fatalf("got %v, want Bubble Tea panic error", result.err)
	}
	assertAlternateScreenRestored(t, result.output)
}

type idleModel struct{}

func (idleModel) Init() tea.Cmd {
	return nil
}

func (m idleModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return m, nil
}

func (idleModel) View() tea.View {
	view := tea.NewView("waiting")
	view.AltScreen = true
	return view
}

func TestBubbleTeaPTYRestoresOnSIGTERM(t *testing.T) {
	master, slave := openTestPTY(t)
	output := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(master)
		output <- string(data)
	}()
	command := exec.Command(os.Args[0], "-test.run=^TestBubbleTeaSIGTERMHelper$")
	command.Stdin = slave
	command.Stdout = slave
	command.Stderr = slave
	command.Env = append(os.Environ(), "BANDWIDTH_TOP_SIGTERM_HELPER=1", "TERM=xterm-256color")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	time.Sleep(150 * time.Millisecond)
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-output:
		assertAlternateScreenRestored(t, value)
	case <-time.After(time.Second):
		t.Fatal("SIGTERM helper output did not close")
	}
}

func TestBubbleTeaSIGTERMHelper(t *testing.T) {
	if os.Getenv("BANDWIDTH_TOP_SIGTERM_HELPER") != "1" {
		return
	}
	if _, err := tea.NewProgram(idleModel{},
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithEnvironment(os.Environ()),
	).Run(); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

type ptyResult struct {
	output string
	err    error
}

func runPTYProgram(
	t *testing.T,
	program *tea.Program,
	master, slave *os.File,
	exit func(),
) ptyResult {
	t.Helper()
	output := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(master)
		output <- string(data)
	}()
	done := make(chan error, 1)
	go func() {
		_, err := program.Run()
		done <- err
	}()
	time.Sleep(100 * time.Millisecond)
	exit()
	select {
	case err := <-done:
		_ = slave.Close()
		select {
		case value := <-output:
			return ptyResult{output: value, err: err}
		case <-time.After(time.Second):
			t.Fatal("PTY output did not close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Bubble Tea program did not exit")
	}
	return ptyResult{}
}

func openTestPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{
		Row: 24,
		Col: 120,
	}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}

func assertAlternateScreenRestored(t *testing.T, output string) {
	t.Helper()
	const enter = "\x1b[?1049h"
	const leave = "\x1b[?1049l"
	if strings.Count(output, enter) != 1 || strings.Count(output, leave) != 1 ||
		strings.Index(output, enter) > strings.Index(output, leave) {
		t.Fatalf("alternate screen lifecycle is unstable: %q", output)
	}
	if regexp.MustCompile(`\x1b\[[0-9;]*[ST]`).MatchString(output) {
		t.Fatalf("alternate screen used scrolling commands: %q", output)
	}
}

func TestComposeFrameCapsPairsAcrossResize(t *testing.T) {
	rows := terminalTestRows(10)
	rows[0].Info.Provider = "TOTAL TRANSIT RX TX"
	status := []string{
		"enrichment: local MMDB -> monitor -> public",
		"local MMDB: ready | monitor: discovered | public: enabled",
	}
	for _, test := range []struct {
		name     string
		size     terminalDimensions
		maxPairs int
		minPairs int
	}{
		{name: "normal", size: terminalDimensions{width: 119, height: 20}, maxPairs: 5, minPairs: 5},
		{name: "shrink", size: terminalDimensions{width: 79, height: 8}, maxPairs: 2, minPairs: 2},
		{name: "grow", size: terminalDimensions{width: 159, height: 30}, maxPairs: 10, minPairs: 9},
	} {
		t.Run(test.name, func(t *testing.T) {
			frame := composeFrame("bandwidth-top", status, rows, bandwidthtop.Totals{}, 99, test.size, true)
			plain := stripTerminalANSI(frame)
			lines := strings.Split(plain, "\n")
			if len(lines) > test.size.height {
				t.Fatalf("height %d produced %d lines:\n%s", test.size.height, len(lines), plain)
			}

			pairs := strings.Count(plain, "=>")
			if pairs < test.minPairs || pairs > test.maxPairs {
				t.Fatalf("height %d produced %d pairs:\n%s", test.size.height, pairs, plain)
			}
			for _, line := range lines {
				if width := uniseg.StringWidth(line); width > test.size.width {
					t.Fatalf("width %d produced %d cells: %q", test.size.width, width, line)
				}
			}
			if !strings.Contains(plain, "TOTAL") {
				t.Fatalf("height %d lost aggregate footer:\n%s", test.size.height, plain)
			}
			if test.size.height == 8 && !strings.HasPrefix(lines[len(lines)-1], "TOTAL") {
				t.Fatalf("short frame selected flow metadata instead of structural footer:\n%s", plain)
			}
		})
	}
}

func TestLookupErrorsBecomeEndpointFreeFrameStatus(t *testing.T) {
	rows := terminalTestRows(3)
	rows[0].Info.Err = "request failed for 198.51.100.20"
	rows[1].Info.Err = "queue full"
	got := lookupErrorStatus(rows)
	if got != "enrichment lookups: 2 unavailable" ||
		strings.Contains(got, "198.51.100.20") {
		t.Fatalf("unexpected lookup status %q", got)
	}
}

func TestSnapshotAndUnsupportedTerminalsNeverAnimate(t *testing.T) {
	frame := composeFrame("bandwidth-top", []string{"enrichment: public fallback"},
		terminalTestRows(1), bandwidthtop.Totals{}, 20, terminalDimensions{width: 80}, false)
	if strings.Contains(frame, "\x1b") {
		t.Fatalf("snapshot contains ANSI: %q", frame)
	}

	var unsupported bytes.Buffer
	if supportsLiveTerminal(&bytes.Buffer{}, false, "xterm-256color") ||
		supportsLiveTerminal(&unsupported, false, "dumb") ||
		supportsLiveTerminal(&unsupported, true, "xterm-256color") {
		t.Fatal("unsupported, dumb, or snapshot output was treated as live")
	}
}

func TestPortSnapshotIsANSIFreeAndModeAware(t *testing.T) {
	rows := terminalTestRows(1)
	rows[0].Port, rows[0].Protocol = "443", "TCP"
	frame := composeFrameForMode(
		"bandwidth-top",
		[]string{"view: ports | rdns: off"},
		rows,
		bandwidthtop.Totals{},
		20,
		terminalDimensions{width: 120},
		false,
		bandwidthtop.ViewPorts,
	)
	if strings.Contains(frame, "\x1b") {
		t.Fatalf("port snapshot contains ANSI: %q", frame)
	}
	for _, want := range []string{"view: ports", "PORT", "PROTO", "443", "TCP"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("port snapshot missing %q:\n%s", want, frame)
		}
	}
}

func TestLiveTerminalSupportsTTYOutputWithRedirectedInput(t *testing.T) {
	_, output := openTestPTY(t)
	if !supportsLiveTerminal(output, false, "xterm-256color") {
		t.Fatal("TTY output was rejected because input may be redirected")
	}
}

func testLiveModel(noResolve bool) (*liveModel, *fakeResolverControl) {
	resolver := &fakeResolverControl{}
	tracker := &fakeLiveTracker{
		errors: make(chan error),
		rows: []talkers.DirectTalkerStat{{
			LocalIP: "192.0.2.10",
			TalkerStat: talkers.TalkerStat{
				IP: "198.51.100.20", Hostname: "cached.example",
			},
		}},
	}
	return newLiveModel(liveModelConfig{
		title: "bandwidth-top", rows: 20, refresh: time.Second,
		noResolve: noResolve, tracker: tracker, enricher: &fakeLiveEnricher{},
		resolver: resolver, initialSize: terminalDimensions{width: 120, height: 24},
		done: make(chan struct{}),
	}), resolver
}

func keyPress(key string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: key, Code: []rune(key)[0]})
}

func updateModel(t *testing.T, model *liveModel, msg tea.Msg) tea.Cmd {
	t.Helper()
	got, cmd := model.Update(msg)
	if got != model {
		t.Fatalf("update returned a different model: %T", got)
	}
	return cmd
}

func terminalTestRows(count int) []bandwidthtop.Row {
	rows := make([]bandwidthtop.Row, 0, count)
	for i := 0; i < count; i++ {
		rows = append(rows, bandwidthtop.Row{
			LocalIP: "192.0.2.10",
			Stat: bandwidthtop.Stat{
				IP: "198.51.100.20",
				Tx: bandwidthtop.RateWindows{Two: float64(1000 + i)},
				Rx: bandwidthtop.RateWindows{Two: float64(2000 + i)},
			},
			Info: bandwidthtop.Enrichment{ASN: 64500, Provider: "Example Networks"},
		})
	}
	return rows
}

var terminalANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func stripTerminalANSI(value string) string {
	return terminalANSI.ReplaceAllString(value, "")
}
