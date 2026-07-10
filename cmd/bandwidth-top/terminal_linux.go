//go:build linux

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"bandwidth-monitor/bandwidthtop"

	"golang.org/x/sys/unix"
)

const (
	enterAlternateScreen = "\x1b[?1049h\x1b[?25l"
	leaveAlternateScreen = "\x1b[0m\x1b[?25h\x1b[?1049l"
	homeCursor           = "\x1b[H"
	clearScreen          = "\x1b[2J"
	clearToScreenEnd     = "\x1b[J"
	defaultWidth         = 120
	defaultHeight        = 24
)

type terminalDimensions struct {
	width  int
	height int
}

type terminalSession struct {
	out     io.Writer
	live    bool
	size    func() terminalDimensions
	mu      sync.Mutex
	entered bool
	closed  bool
}

func newTerminalSession(out io.Writer, live bool, size func() terminalDimensions) *terminalSession {
	if size == nil {
		size = func() terminalDimensions { return terminalDimensions{defaultWidth, defaultHeight} }
	}
	return &terminalSession{out: out, live: live, size: size}
}

func (t *terminalSession) withScreen(run func() error) (err error) {
	if err := t.enter(); err != nil {
		_ = t.close()
		return err
	}
	defer func() {
		closeErr := t.close()
		if err == nil {
			err = closeErr
		}
	}()
	return run()
}

func (t *terminalSession) enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.live || t.entered {
		return nil
	}
	t.entered = true
	if _, err := io.WriteString(t.out, enterAlternateScreen); err != nil {
		return err
	}
	return nil
}

func (t *terminalSession) draw(frame string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	frame = strings.TrimRight(frame, "\n")
	if !t.live {
		_, err := io.WriteString(t.out, frame+"\n")
		return err
	}
	if !t.entered || t.closed {
		return fmt.Errorf("terminal screen is not active")
	}
	// One write keeps a frame contiguous. Reserving the final terminal column
	// in dimensions prevents bottom-right autowrap; J erases stale old lines.
	_, err := io.WriteString(t.out, homeCursor+clearScreen+homeCursor+frame+clearToScreenEnd)
	return err
}

func (t *terminalSession) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.live || !t.entered || t.closed {
		return nil
	}
	t.closed = true
	_, err := io.WriteString(t.out, leaveAlternateScreen)
	return err
}

func (t *terminalSession) dimensions(configuredWidth int) terminalDimensions {
	size := t.size()
	if size.width <= 0 {
		size.width = defaultWidth
	}
	if size.height <= 0 {
		size.height = defaultHeight
	}
	if configuredWidth > 0 && (!t.live || configuredWidth < size.width) {
		size.width = configuredWidth
	}
	if t.live && size.width > 1 {
		size.width--
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
	for len(status) > 1 && 1+len(status)+7 > height {
		status = status[:len(status)-1]
	}
	if 1+len(status)+7 <= height {
		pairLimit := maxInt(0, (height-1-len(status)-7)/2)
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

// Very short terminals preserve title, column identity, and TOTAL first. The
// fallback chain, complete pairs, and TX/RX details are added as space permits.
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
	if headerIndex >= 0 {
		header = rendered[headerIndex]
	}
	footerStart := len(rendered) - 5
	if footerStart >= 0 {
		total = rendered[footerStart+3]
	}
	lines := []string{title}
	remaining := height - 1
	if remaining == 1 {
		if total != "" {
			lines = append(lines, total)
		}
		return strings.Join(lines, "\n")
	}
	if len(status) > 0 && remaining >= 3 {
		lines = append(lines, status[0])
		remaining--
	}
	if header != "" && remaining >= 2 {
		lines = append(lines, header)
		remaining--
	}
	pairStart := headerIndex + 1
	for pair := 0; pair < len(rows) && pairStart >= 1 &&
		pairStart+1 < len(rendered) && remaining >= 3; pair++ {
		lines = append(lines, rendered[pairStart], rendered[pairStart+1])
		pairStart += 2
		remaining -= 2
	}
	for offset := 1; offset <= 2; offset++ {
		if remaining <= 1 {
			break
		}
		if footerStart >= 0 {
			lines = append(lines, rendered[footerStart+offset])
			remaining--
		}
	}
	if remaining > 0 && total != "" {
		lines = append(lines, total)
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
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}

func terminalSize(out io.Writer) terminalDimensions {
	file, ok := out.(*os.File)
	if !ok {
		return terminalDimensions{defaultWidth, defaultHeight}
	}
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil || size.Col == 0 || size.Row == 0 {
		return terminalDimensions{defaultWidth, defaultHeight}
	}
	return terminalDimensions{width: int(size.Col), height: int(size.Row)}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
