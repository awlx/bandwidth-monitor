package handler

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSharedPayloadBuildsOncePerWindow(t *testing.T) {
	var builds atomic.Int32
	now := time.Unix(1000, 0)
	s := newSharedPayload(900*time.Millisecond, func() ([]byte, error) {
		builds.Add(1)
		return []byte("x"), nil
	})
	s.now = func() time.Time { return now }

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.get(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := builds.Load(); got != 1 {
		t.Fatalf("50 concurrent clients built %d payloads, want 1", got)
	}

	now = now.Add(899 * time.Millisecond)
	s.get()
	if got := builds.Load(); got != 1 {
		t.Fatalf("payload rebuilt within max age (%d builds)", got)
	}
	now = now.Add(time.Millisecond)
	s.get()
	if got := builds.Load(); got != 2 {
		t.Fatalf("payload not rebuilt after max age (%d builds)", got)
	}
}

func TestSharedPayloadDoesNotCacheErrors(t *testing.T) {
	fail := true
	s := newSharedPayload(time.Hour, func() ([]byte, error) {
		if fail {
			return nil, errors.New("boom")
		}
		return []byte("ok"), nil
	})
	if _, err := s.get(); err == nil {
		t.Fatal("expected error")
	}
	fail = false
	if data, err := s.get(); err != nil || string(data) != "ok" {
		t.Fatalf("get after failure = %q, %v", data, err)
	}
}
