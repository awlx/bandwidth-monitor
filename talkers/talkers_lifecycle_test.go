package talkers

import (
	"errors"
	"sync"
	"testing"
)

func TestStopIsSafeBeforeRun(t *testing.T) {
	tracker := lifecycleTestTracker(nil)

	const callers = 16
	var stops sync.WaitGroup
	stops.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer stops.Done()
			tracker.Stop()
		}()
	}
	stops.Wait()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		tracker.Run()
	}()
	<-runDone
}

func TestStopWaitsForCaptureWorker(t *testing.T) {
	captureStarted := make(chan struct{})
	stopObserved := make(chan struct{})
	releaseCapture := make(chan struct{})
	var tracker *Tracker
	tracker = lifecycleTestTracker(func(string) error {
		close(captureStarted)
		<-tracker.stopCh
		close(stopObserved)
		<-releaseCapture
		return nil
	})

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		tracker.Run()
	}()
	<-captureStarted

	const callers = 16
	stopDone := make(chan struct{}, callers)
	for i := 0; i < callers; i++ {
		go func() {
			tracker.Stop()
			stopDone <- struct{}{}
		}()
	}

	<-stopObserved
	select {
	case <-stopDone:
		t.Fatal("Stop returned while a capture worker was still active")
	default:
	}

	close(releaseCapture)
	for i := 0; i < callers; i++ {
		<-stopDone
	}
	<-runDone
}

func lifecycleTestTracker(captureFn func(string) error) *Tracker {
	if captureFn == nil {
		captureFn = func(string) error {
			panic("capture worker started unexpectedly")
		}
	}
	return &Tracker{
		devices:    []string{"capture-test"},
		lanDevices: map[string]bool{"capture-test": true},
		buckets:    make([]*bucket, 0, 1),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		errCh:      make(chan error, 1),
		captureFn:  captureFn,
	}
}

func TestDirectCaptureOptsIntoNonLANInterface(t *testing.T) {
	started := make(chan struct{})
	var tracker *Tracker
	tracker = lifecycleTestTracker(func(string) error {
		close(started)
		<-tracker.stopCh
		return nil
	})
	tracker.direct = true
	tracker.lanDevices = map[string]bool{}

	go tracker.Run()
	<-started
	tracker.Stop()
}

func TestCaptureErrorIsReported(t *testing.T) {
	want := errors.New("capture unavailable")
	tracker := lifecycleTestTracker(func(string) error { return want })

	go tracker.Run()
	if got := <-tracker.Errors(); !errors.Is(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	tracker.Stop()
}

func TestMonitorDeviceCapturedWithoutLANAddress(t *testing.T) {
	started := make(chan string, 4)
	var tracker *Tracker
	tracker = lifecycleTestTracker(func(dev string) error {
		started <- dev
		<-tracker.stopCh
		return nil
	})
	// INTERFACES lists only a display interface; the SPAN port has no
	// address, is not LAN-facing, and must still be captured.
	tracker.devices = []string{"eth0"}
	tracker.lanDevices = map[string]bool{}
	tracker.SetMonitorDevices([]string{"span0"})

	go tracker.Run()
	if dev := <-started; dev != "span0" {
		t.Fatalf("captured %q, want span0", dev)
	}
	tracker.Stop()
	select {
	case dev := <-started:
		t.Fatalf("unexpected capture on %q", dev)
	default:
	}
}

func TestWithMonitorDevicesDeduplicates(t *testing.T) {
	tr := &Tracker{}
	tr.SetMonitorDevices([]string{"span1", "eth1", "span0"})
	got := tr.withMonitorDevices([]string{"eth0", "eth1"})
	want := []string{"eth0", "eth1", "span0", "span1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}
