//go:build darwin

package main

import (
	"errors"
	"strings"
	"testing"
)

func TestDarwinCaptureErrorExplainsBPFAccess(t *testing.T) {
	err := captureError(errors.New("permission denied"))
	if !strings.Contains(err.Error(), "/dev/bpf") || !strings.Contains(err.Error(), "root") {
		t.Fatalf("unexpected capture error: %v", err)
	}
}
