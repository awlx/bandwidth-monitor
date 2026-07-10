//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"

	"bandwidth-monitor/bandwidthtop"

	"github.com/rivo/uniseg"
)

type recordingWriter struct {
	writes []string
	failAt int
}

func (w *recordingWriter) Write(value []byte) (int, error) {
	w.writes = append(w.writes, string(value))
	if w.failAt > 0 && len(w.writes) == w.failAt {
		return 0, io.ErrClosedPipe
	}
	return len(value), nil
}

func TestLiveTerminalUsesStableAlternateScreenFrames(t *testing.T) {
	writer := &recordingWriter{}
	session := newTerminalSession(writer, true, nil)
	if err := session.withScreen(func() error {
		if err := session.draw("first\nlong stale line"); err != nil {
			return err
		}
		return session.draw("second")
	}); err != nil {
		t.Fatal(err)
	}
	if len(writer.writes) != 4 {
		t.Fatalf("got %d writes, want enter, two atomic frames, restore: %q", len(writer.writes), writer.writes)
	}
	if writer.writes[0] != enterAlternateScreen || writer.writes[3] != leaveAlternateScreen {
		t.Fatalf("alternate-screen lifecycle missing: %q", writer.writes)
	}
	for _, frame := range writer.writes[1:3] {
		if !strings.HasPrefix(frame, homeCursor+clearScreen+homeCursor) ||
			!strings.HasSuffix(frame, clearToScreenEnd) {
			t.Fatalf("frame does not redraw and clear stale remainder: %q", frame)
		}
		content := strings.TrimSuffix(
			strings.TrimPrefix(frame, homeCursor+clearScreen+homeCursor),
			clearToScreenEnd)
		if strings.HasSuffix(content, "\n") {
			t.Fatalf("frame has scroll-triggering trailing newline: %q", frame)
		}
	}
}

func TestTerminalRestoresScreenOnEveryExit(t *testing.T) {
	sentinel := errors.New("capture failed")
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "normal"},
		{name: "signal"},
		{name: "error", err: sentinel},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingWriter{}
			session := newTerminalSession(writer, true, nil)
			got := session.withScreen(func() error { return test.err })
			if !errors.Is(got, test.err) || len(writer.writes) != 2 ||
				writer.writes[0] != enterAlternateScreen || writer.writes[1] != leaveAlternateScreen {
				t.Fatalf("err=%v writes=%q", got, writer.writes)
			}
			if err := session.close(); err != nil || len(writer.writes) != 2 {
				t.Fatalf("cleanup was not idempotent: err=%v writes=%q", err, writer.writes)
			}
		})
	}
}

func TestTerminalRestoresAfterFrameWriteError(t *testing.T) {
	writer := &recordingWriter{failAt: 2}
	session := newTerminalSession(writer, true, nil)
	err := session.withScreen(func() error { return session.draw("frame") })
	if !errors.Is(err, io.ErrClosedPipe) || len(writer.writes) != 3 ||
		writer.writes[2] != leaveAlternateScreen {
		t.Fatalf("err=%v writes=%q", err, writer.writes)
	}
}

func TestTerminalRestoresAfterPanic(t *testing.T) {
	writer := &recordingWriter{}
	session := newTerminalSession(writer, true, nil)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		_ = session.withScreen(func() error {
			panic("boom")
		})
	}()
	if len(writer.writes) != 2 || writer.writes[1] != leaveAlternateScreen {
		t.Fatalf("screen was not restored after panic: %q", writer.writes)
	}
}

func TestTerminalAttemptsRestoreAfterEnterError(t *testing.T) {
	writer := &recordingWriter{failAt: 1}
	session := newTerminalSession(writer, true, nil)
	err := session.withScreen(func() error {
		t.Fatal("run called after enter failure")
		return nil
	})
	if !errors.Is(err, io.ErrClosedPipe) || len(writer.writes) != 2 ||
		writer.writes[1] != leaveAlternateScreen {
		t.Fatalf("err=%v writes=%q", err, writer.writes)
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
	writer := &recordingWriter{}
	session := newTerminalSession(writer, false, nil)
	if err := session.withScreen(func() error { return session.draw(frame) }); err != nil {
		t.Fatal(err)
	}
	if len(writer.writes) != 1 || strings.Contains(writer.writes[0], "\x1b") {
		t.Fatalf("non-TTY output animated: %q", writer.writes)
	}
	var unsupported bytes.Buffer
	if supportsLiveTerminal(&bytes.Buffer{}, false, "xterm-256color") ||
		supportsLiveTerminal(&unsupported, false, "dumb") ||
		supportsLiveTerminal(&unsupported, true, "xterm-256color") {
		t.Fatal("unsupported, dumb, or snapshot output was treated as live")
	}
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
