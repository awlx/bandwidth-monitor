//go:build darwin

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinCaptureErrorExplainsBPFAccess(t *testing.T) {
	err := captureError(fmt.Errorf("open BPF device: %w", unix.EACCES))
	if !strings.Contains(err.Error(), "/dev/bpf") || !strings.Contains(err.Error(), "root") {
		t.Fatalf("unexpected capture error: %v", err)
	}
}

func TestDarwinCaptureErrorDoesNotMislabelConfigurationFailure(t *testing.T) {
	err := captureError(errors.New("configure BPF: bad address"))
	if strings.Contains(err.Error(), "/dev/bpf") || strings.Contains(err.Error(), "root") {
		t.Fatalf("configuration error included a privilege hint: %v", err)
	}
}
