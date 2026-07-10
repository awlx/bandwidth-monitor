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
		!strings.Contains(stderr.String(), "CAP_NET_RAW") {
		t.Fatalf("unexpected help:\n%s", stderr.String())
	}
}

func TestOutputWidthUsesExplicitValue(t *testing.T) {
	if got := outputWidth(&bytes.Buffer{}, 73); got != 73 {
		t.Fatalf("got %d", got)
	}
}
