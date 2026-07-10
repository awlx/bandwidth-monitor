//go:build linux

package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"
)

func TestHelpDoesNotStartCapture(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--help"}, &stdout, &stderr)
	if err != flag.ErrHelp {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage: bandwidth-top") ||
		!strings.Contains(stderr.String(), "CAP_NET_RAW") ||
		!strings.Contains(stderr.String(), "checked once at startup") ||
		!strings.Contains(stderr.String(), "local-network") {
		t.Fatalf("unexpected help:\n%s", stderr.String())
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
	session := newTerminalSession(&bytes.Buffer{}, true, func() terminalDimensions {
		return terminalDimensions{width: 100, height: 30}
	})
	if got := session.dimensions(73); got.width != 72 || got.height != 30 {
		t.Fatalf("got %+v", got)
	}
	if got := session.dimensions(200); got.width != 99 || got.height != 30 {
		t.Fatalf("live width exceeded terminal: %+v", got)
	}

	snapshot := newTerminalSession(&bytes.Buffer{}, false, func() terminalDimensions {
		return terminalDimensions{width: 120, height: 24}
	})
	if got := snapshot.dimensions(200); got.width != 200 || got.height != 24 {
		t.Fatalf("snapshot ignored explicit width: %+v", got)
	}
}
