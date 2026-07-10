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

func TestOutputWidthUsesExplicitValue(t *testing.T) {
	if got := outputWidth(&bytes.Buffer{}, 73); got != 73 {
		t.Fatalf("got %d", got)
	}
}
