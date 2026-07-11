//go:build linux || darwin

package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"

	"bandwidth-monitor/version"
)

func TestHelpDoesNotStartCapture(t *testing.T) {
	originalVersion, originalCommit := version.Version, version.Commit
	t.Cleanup(func() { version.Version, version.Commit = originalVersion, originalCommit })
	version.Version, version.Commit = "1.2.3", "ignored"
	var stdout, stderr bytes.Buffer
	err := run([]string{"--help"}, &stdout, &stderr)
	if err != flag.ErrHelp {
		t.Fatalf("got %v", err)
	}
	if !strings.HasPrefix(stderr.String(), "bandwidth-top 1.2.3\n") ||
		!strings.Contains(stderr.String(), "Usage: bandwidth-top") ||
		!strings.Contains(stderr.String(), captureUsageText()) ||
		!strings.Contains(stderr.String(), "checked once at startup") ||
		!strings.Contains(stderr.String(), "local-network") ||
		!strings.Contains(stderr.String(), "ports") ||
		!strings.Contains(stderr.String(), "no-resolve") ||
		!strings.Contains(stderr.String(), "-i") ||
		!strings.Contains(stderr.String(), "-L") ||
		!strings.Contains(stderr.String(), "-t") ||
		!strings.Contains(stderr.String(), "-P") ||
		!strings.Contains(stderr.String(), "-n") ||
		!strings.Contains(stderr.String(), "-v") {
		t.Fatalf("unexpected help:\n%s", stderr.String())
	}
}

func TestShortAndLongOptionsAreEquivalent(t *testing.T) {
	long, err := parseCLIOptions([]string{
		"--interface", "test0", "--rows", "7", "--snapshot", "--ports", "--no-resolve",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	short, err := parseCLIOptions([]string{
		"-i", "test0", "-L", "7", "-t", "-P", "-n",
	}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if long.interfaceName != short.interfaceName || long.rows != short.rows ||
		long.snapshot != short.snapshot || long.ports != short.ports ||
		long.noResolve != short.noResolve {
		t.Fatalf("long=%+v short=%+v", long, short)
	}
}

func TestVersionOptionsExitWithoutCapture(t *testing.T) {
	originalVersion, originalCommit := version.Version, version.Commit
	t.Cleanup(func() { version.Version, version.Commit = originalVersion, originalCommit })
	version.Version, version.Commit = "2.3.4", "ignored"
	for _, option := range []string{"-v", "--version"} {
		t.Run(option, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run([]string{option, "--interface", "does-not-exist"}, &stdout, &stderr)
			if err != nil || stdout.String() != "bandwidth-top 2.3.4\n" || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q err=%v", stdout.String(), stderr.String(), err)
			}
		})
	}
}

func TestInvalidLocalNetworkFailsBeforeCapture(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--local-network", "not-a-cidr"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "invalid local network") {
		t.Fatalf("got %v", err)
	}
}

func TestTerminalDimensionsUseConfiguredUpperBound(t *testing.T) {
	if got := liveDimensions(terminalDimensions{width: 100, height: 30}, 73); got.width != 72 || got.height != 30 {
		t.Fatalf("got %+v", got)
	}
	if got := liveDimensions(terminalDimensions{width: 100, height: 30}, 200); got.width != 99 || got.height != 30 {
		t.Fatalf("live width exceeded terminal: %+v", got)
	}

	if got := snapshotDimensions(&bytes.Buffer{}, 200); got.width != 200 || got.height != 24 {
		t.Fatalf("snapshot ignored explicit width: %+v", got)
	}
}
