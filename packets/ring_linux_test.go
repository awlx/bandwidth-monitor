//go:build linux

package packets

import (
	"errors"
	"fmt"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxPollInterruptionIsRetryable(t *testing.T) {
	if err := normalizeLinuxPollError(fmt.Errorf("poll: %w", unix.EINTR)); err != nil {
		t.Fatalf("EINTR remained fatal: %v", err)
	}
	sentinel := errors.New("poll failed")
	if err := normalizeLinuxPollError(sentinel); !errors.Is(err, sentinel) {
		t.Fatalf("non-EINTR error was discarded: %v", err)
	}
}
